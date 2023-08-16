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
	"os"

	"github.com/kelseyhightower/envconfig"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	"github.com/submariner-io/admiral/pkg/names"
	admversion "github.com/submariner-io/admiral/pkg/version"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/event/controller"
	eventlogger "github.com/submariner-io/submariner/pkg/event/logger"
	"github.com/submariner-io/submariner/pkg/networkplugin-syncer/handlers/ovn"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/environment"
	"github.com/submariner-io/submariner/pkg/versions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
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

	admversion.Print(names.NetworkPluginSyncerComponent, versions.Submariner())

	if showVersion {
		return
	}

	kzerolog.InitK8sLogging()

	versions.Log(&logger)

	logger.Info("Starting submariner-networkplugin-syncer")
	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler().Done()

	var env environment.Specification

	err := envconfig.Process("submariner", &env)
	logger.FatalOnError(err, "Error processing env config")

	networkPlugin := os.Getenv("SUBMARINER_NETWORKPLUGIN")

	if networkPlugin == "" {
		networkPlugin = cni.Generic
	}

	registry := event.NewRegistry("networkplugin-syncer", networkPlugin)
	err = registry.AddHandlers(eventlogger.NewHandler(), ovn.NewSyncHandler(getK8sClient(), &env))
	logger.FatalOnError(err, "Error registering the handlers")

	if env.Uninstall {
		if err := registry.StopHandlers(true); err != nil {
			logger.Warningf("Error stopping handlers: %v", err)
		}

		return
	}

	ctl, err := controller.New(&controller.Config{
		Registry:   registry,
		MasterURL:  masterURL,
		Kubeconfig: kubeconfig,
	})
	logger.FatalOnError(err, "Error creating controller for event handling")

	err = ctl.Start(stopCh)
	logger.FatalOnError(err, "Error starting controller")

	<-stopCh
	ctl.Stop()

	logger.Info("All controllers stopped or exited. Stopping submariner-networkplugin-syncer")
}

func getK8sClient() kubernetes.Interface {
	var cfg *rest.Config
	var err error

	if masterURL == "" && kubeconfig == "" {
		cfg, err = rest.InClusterConfig()
		logger.FatalOnError(err, "Error getting in-cluster-config, please set kubeconfig and master parameters")
	} else {
		cfg, err = clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
		logger.FatalOnError(err, "Error building kubeconfig")
	}

	clientSet, err := kubernetes.NewForConfig(cfg)
	logger.FatalOnError(err, "Error building clientset")

	return clientSet
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
	flag.BoolVar(&showVersion, "version", showVersion, "Show version")
}
