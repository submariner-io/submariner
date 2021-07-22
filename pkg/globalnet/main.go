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
	"context"
	"flag"
	"net/http"
	"os"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
	"k8s.io/client-go/kubernetes/scheme"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
)

var (
	masterURL  string
	kubeconfig string
)

func main() {
	klog.InitFlags(nil)
	flag.Parse()
	var spec controllers.Specification

	err := envconfig.Process("submariner", &spec)
	if err != nil {
		klog.Fatal(err)
	}

	klog.Info("Starting submariner-globalnet", spec)

	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler().Done()

	httpServer := startHTTPServer()

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s", err.Error())
	}

	submarinerClient, err := submarinerClientset.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("error building submariner clientset: %s", err.Error())
	}

	err = mcsv1a1.AddToScheme(scheme.Scheme)
	if err != nil {
		klog.Fatalf("Error adding Multicluster v1alpha1 to the scheme: %v", err)
	}

	err = submarinerv1.AddToScheme(scheme.Scheme)
	if err != nil {
		klog.Fatalf("Error adding submariner to the scheme: %v", err)
	}

	var localCluster *submarinerv1.Cluster
	// During installation, sometimes creation of clusterCRD by submariner-gateway-pod would take few secs.
	for i := 0; i < 100; i++ {
		localCluster, err = submarinerClient.SubmarinerV1().Clusters(spec.Namespace).Get(context.TODO(), spec.ClusterID,
			metav1.GetOptions{})
		if err == nil {
			break
		}

		time.Sleep(3 * time.Second)
	}

	if err != nil {
		klog.Fatalf("error while retrieving the local cluster %q info even after waiting for 5 mins: %v", spec.ClusterID, err)
	}

	if localCluster.Spec.GlobalCIDR != nil && len(localCluster.Spec.GlobalCIDR) > 0 {
		// TODO: Revisit when support for more than one globalCIDR is implemented.
		spec.GlobalCIDR = localCluster.Spec.GlobalCIDR
	} else {
		klog.Errorf("Cluster %s is not configured to use globalCidr", spec.ClusterID)
		os.Exit(1)
	}

	gatewayMonitor, err := controllers.NewGatewayMonitor(spec, append(localCluster.Spec.ClusterCIDR, localCluster.Spec.ServiceCIDR...),
		watcher.Config{RestConfig: cfg})

	if err != nil {
		klog.Fatalf("Error creating gatewayMonitor: %s", err.Error())
	}

	if err = gatewayMonitor.Start(); err != nil {
		klog.Fatalf("Error running gatewayMonitor: %s", err.Error())
	}

	<-stopCh
	gatewayMonitor.Stop()

	klog.Infof("All controllers stopped or exited. Stopping main loop")

	if err := httpServer.Shutdown(context.TODO()); err != nil {
		klog.Errorf("Error shutting down metrics HTTP server: %v", err)
	}
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}

func startHTTPServer() *http.Server {
	srv := &http.Server{Addr: ":8081"}

	http.Handle("/metrics", promhttp.Handler())

	go func() {
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			klog.Errorf("Error starting metrics server: %v", err)
		}
	}()

	return srv
}
