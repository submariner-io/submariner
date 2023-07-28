/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/kelseyhightower/envconfig"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	cni "github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/event/controller"
	eventlogger "github.com/submariner-io/submariner/pkg/event/logger"
	"github.com/submariner-io/submariner/pkg/node"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/cabledriver"
	cniapi "github.com/submariner-io/submariner/pkg/routeagent_driver/cni"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/environment"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/calico"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/kubeproxy"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/mtu"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/ovn"
	"github.com/submariner-io/submariner/pkg/versions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

var (
	masterURL  string
	kubeconfig string
	logger     = log.Logger{Logger: logf.Log.WithName("main")}
)

func main() {
	kzerolog.AddFlags(nil)
	flag.Parse()
	kzerolog.InitK8sLogging()

	versions.Log(&logger)

	logger.Info("Starting submariner-route-agent using the event framework")
	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler().Done()

	var env environment.Specification

	err := envconfig.Process("submariner", &env)
	if err != nil {
		logger.Fatalf("Error reading the environment variables: %s", err.Error())
	}

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
	if err != nil {
		logger.Fatalf("Error building kubeconfig: %s", err.Error())
	}

	k8sClientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		logger.Fatalf("Error building clientset: %s", err.Error())
	}

	err = v1.AddToScheme(scheme.Scheme)
	logger.FatalOnError(err, "Error adding submariner to the scheme")

	smClientset, err := submarinerClientset.NewForConfig(cfg)
	if err != nil {
		logger.Fatalf("Error building submariner clientset: %s", err.Error())
	}

	np := os.Getenv("SUBMARINER_NETWORKPLUGIN")

	if np == "" {
		np = cni.Generic
	}

	registry := event.NewRegistry("routeagent_driver", np)
	if err := registry.AddHandlers(
		eventlogger.NewHandler(),
		kubeproxy.NewSyncHandler(env.ClusterCidr, env.ServiceCidr),
		ovn.NewHandler(&env, smClientset),
		ovn.NewGatewayRouteHandler(&env, smClientset),
		ovn.NewNonGatewayRouteHandler(smClientset, k8sClientSet),
		cabledriver.NewXRFMCleanupHandler(),
		cabledriver.NewVXLANCleanup(),
		mtu.NewMTUHandler(env.ClusterCidr, len(env.GlobalCidr) != 0, getTCPMssValue(k8sClientSet)),
		calico.NewCalicoIPPoolHandler(cfg),
	); err != nil {
		logger.Fatalf("Error registering the handlers: %s", err.Error())
	}

	if env.Uninstall {
		if err := registry.StopHandlers(true); err != nil {
			logger.Warningf("Error stopping handlers: %v", err)
		}

		if err = annotateNode([]string{}, k8sClientSet); err != nil {
			logger.Warningf("Error removing %q annotation: %v", constants.CNIInterfaceIP, err)
		}

		return
	}

	if err = annotateNode(env.ClusterCidr, k8sClientSet); err != nil {
		logger.Errorf(err, "Error while annotating the node")
	}

	ctl, err := controller.New(&controller.Config{
		Registry:   registry,
		MasterURL:  masterURL,
		Kubeconfig: kubeconfig,
	})
	if err != nil {
		logger.Fatalf("Error creating controller for event handling %v", err)
	}

	err = ctl.Start(stopCh)
	if err != nil {
		logger.Fatalf("Error starting controller: %v", err)
	}

	<-stopCh
	ctl.Stop()

	logger.Info("All controllers stopped or exited. Stopping submariner-route-agent")
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}

func annotateNode(clusterCidr []string, k8sClientSet *kubernetes.Clientset) error {
	nodeName, ok := os.LookupEnv("NODE_NAME")
	if !ok {
		return fmt.Errorf("error reading the NODE_NAME from the environment")
	}

	err := cniapi.AnnotateNodeWithCNIInterfaceIP(nodeName, k8sClientSet, clusterCidr)
	if err != nil {
		return errors.Wrap(err, "error annotating node with CNI interface IP")
	}

	return nil
}

func getTCPMssValue(k8sClientSet *kubernetes.Clientset) int {
	localNode, err := node.GetLocalNode(k8sClientSet)
	if err != nil {
		logger.Errorf(err, "Error getting information on the local node")
		return 0
	}

	tcpMssStr := localNode.GetAnnotations()[v1.TCPMssValue]

	if tcpMssStr == "" {
		return 0
	}

	tcpMssValue, err := strconv.Atoi(tcpMssStr)
	if err != nil {
		logger.Errorf(err, "Error parsing %q annotation", v1.TCPMssValue)
		return 0
	}

	return tcpMssValue
}
