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
	"github.com/submariner-io/submariner/pkg/routeagent_driver/environment"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/event/controller"
	"github.com/submariner-io/submariner/pkg/event/logger"
	"github.com/submariner-io/submariner/pkg/networkplugin-syncer/handlers/ovn"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"

	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

var (
	masterURL  string
	kubeconfig string
)

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	klog.Info("Starting submariner-networkplugin-syncer")
	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler().Done()

	var env environment.Specification
	err := envconfig.Process("submariner", &env)
	if err != nil {
		klog.Fatalf("Error reading the environment variables: %s", err.Error())
	}

	networkPlugin := os.Getenv("SUBMARINER_NETWORKPLUGIN")

	if networkPlugin == "" {
		networkPlugin = constants.NetworkPluginGeneric
	}

	registry := event.NewRegistry("networkplugin-syncer", networkPlugin)
	if err := registry.AddHandlers(logger.NewHandler(), ovn.NewSyncHandler(getK8sClient(), env)); err != nil {
		klog.Fatalf("Error registering the handlers: %s", err.Error())
	}

	ctl, err := controller.New(&controller.Config{
		Registry:   registry,
		MasterURL:  masterURL,
		Kubeconfig: kubeconfig})

	if err != nil {
		klog.Fatalf("Error creating controller for event handling %v", err)
	}

	err = ctl.Start(stopCh)
	if err != nil {
		klog.Fatalf("Error starting controller: %v", err)
	}

	<-stopCh
	ctl.Stop()

	klog.Info("All controllers stopped or exited. Stopping submariner-networkplugin-syncer")
}

func getK8sClient() kubernetes.Interface {
	var cfg *rest.Config
	var err error
	if masterURL == "" && kubeconfig == "" {
		cfg, err = rest.InClusterConfig()
		if err != nil {
			klog.Fatalf("Error getting in-cluster-config, please set kubeconfig && master parameters")
		}
	} else {
		cfg, err = clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
		if err != nil {
			klog.Fatalf("Error building kubeconfig: %s", err.Error())
		}
	}

	clientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building clientset: %s", err.Error())
	}

	return clientSet
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}
