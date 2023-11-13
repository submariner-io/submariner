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
	"io/fs"
	"os"
	"strconv"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	"github.com/submariner-io/admiral/pkg/names"
	admversion "github.com/submariner-io/admiral/pkg/version"
	"github.com/submariner-io/admiral/pkg/watcher"
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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	nodeutil "k8s.io/component-helpers/node/util"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

var (
	masterURL   string
	kubeconfig  string
	logger      = log.Logger{Logger: logf.Log.WithName("main")}
	showVersion = false
)

func main() {
	kzerolog.AddFlags(nil)
	flag.Parse()

	admversion.Print(names.RouteAgentComponent, versions.Submariner())

	if showVersion {
		return
	}

	kzerolog.InitK8sLogging()

	versions.Log(&logger)

	logger.Info("Starting submariner-route-agent using the event framework")
	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler().Done()

	// Clean up "sockets" created as directories by previous versions
	removeInvalidSockets()

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

	dynamicClientSet, err := dynamic.NewForConfig(cfg)
	if err != nil {
		logger.Fatalf("Error building dynamic client: %s", err.Error())
	}

	err = v1.AddToScheme(scheme.Scheme)
	logger.FatalOnError(err, "Error adding submariner to the scheme")

	smClientset, err := submarinerClientset.NewForConfig(cfg)
	if err != nil {
		logger.Fatalf("Error building submariner clientset: %s", err.Error())
	}

	if env.WaitForNode {
		waitForNodeReady(k8sClientSet)

		return
	}

	np := os.Getenv("SUBMARINER_NETWORKPLUGIN")

	if np == "" {
		np = cni.Generic
	}

	config := &watcher.Config{RestConfig: cfg}

	registry := event.NewRegistry("routeagent_driver", np)
	if err := registry.AddHandlers(
		eventlogger.NewHandler(),
		kubeproxy.NewSyncHandler(env.ClusterCidr, env.ServiceCidr),
		ovn.NewHandler(&env, smClientset, k8sClientSet, dynamicClientSet, config),
		ovn.NewGatewayRouteHandler(smClientset),
		ovn.NewNonGatewayRouteHandler(smClientset, k8sClientSet),
		cabledriver.NewXRFMCleanupHandler(),
		cabledriver.NewVXLANCleanup(),
		mtu.NewMTUHandler(env.ClusterCidr, len(env.GlobalCidr) != 0, getTCPMssValue(k8sClientSet)),
		calico.NewCalicoIPPoolHandler(cfg),
	); err != nil {
		logger.Fatalf("Error registering the handlers: %s", err.Error())
	}

	if env.Uninstall {
		uninstall(k8sClientSet, registry)

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
	flag.BoolVar(&showVersion, "version", showVersion, "Show version")
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

func uninstall(k8sClientSet *kubernetes.Clientset, registry *event.Registry) {
	if err := registry.StopHandlers(); err != nil {
		logger.Warningf("Error stopping handlers: %v", err)
	}

	if err := registry.Uninstall(); err != nil {
		logger.Warningf("Error uninstalling handlers: %v", err)
	}

	if err := annotateNode([]string{}, k8sClientSet); err != nil {
		logger.Warningf("Error removing %q annotation: %v", constants.CNIInterfaceIP, err)
	}
}

func waitForNodeReady(k8sClientSet *kubernetes.Clientset) {
	// In most cases the node will already be ready; otherwise, wait for ever
	for {
		localNode, err := node.GetLocalNode(k8sClientSet)

		if err != nil {
			logger.Error(err, "Error retrieving local node")
		} else if localNode != nil {
			_, condition := nodeutil.GetNodeCondition(&localNode.Status, corev1.NodeReady)
			if condition != nil && condition.Status == corev1.ConditionTrue {
				logger.Info("Node ready")
				return
			}

			logger.Infof("Node not ready, waiting: %v", localNode.Status)
		}

		time.Sleep(1 * time.Second)
	}
}

func removeInvalidSockets() {
	// This can be removed once we stop supporting upgrades from 0.16.0 or older
	for _, dir := range []string{"/run/openvswitch/db.sock", "/var/run/openvswitch/ovnnb_db.sock", "/var/run/ovn-ic/ovnnb_db.sock"} {
		info, err := os.Stat(dir)
		if (err == nil || errors.Is(err, fs.ErrExist)) && info.IsDir() {
			err := os.Remove(dir)
			if err != nil {
				logger.Errorf(err, "Failed to delete invalid socket %s", dir)
			} else {
				logger.Infof("Deleted invalid socket %s", dir)
			}
		}
	}
}
