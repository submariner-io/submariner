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
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cidr"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
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

	var spec controllers.Specification

	err := envconfig.Process("submariner", &spec)
	logger.FatalOnError(err, "Error processing env config")

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
	logger.FatalOnError(err, "Error building kube config")

	submarinerClient, err := submarinerClientset.NewForConfig(cfg)
	logger.FatalOnError(err, "Error building submariner clientse")

	if spec.Uninstall {
		logger.Info("Uninstalling submariner-globalnet")
		controllers.UninstallDataPath()
		controllers.DeleteGlobalnetObjects(submarinerClient, cfg)
		controllers.RemoveGlobalIPAnnotationOnNode(cfg)

		return
	}

	logger.Info("Starting submariner-globalnet", spec)

	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler().Done()

	httpServer := startHTTPServer(spec)

	err = mcsv1a1.AddToScheme(scheme.Scheme)
	logger.FatalOnError(err, "Error adding Multicluster v1alpha1 to the scheme")

	err = submarinerv1.AddToScheme(scheme.Scheme)
	logger.FatalOnError(err, "Error adding submariner to the scheme")

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

	logger.FatalfOnError(err, "Error while retrieving the local cluster %q info even after waiting for 5 mins", spec.ClusterID)

	if localCluster.Spec.GlobalCIDR != nil && len(localCluster.Spec.GlobalCIDR) > 0 {
		// TODO: Revisit when support for more than one globalCIDR is implemented.
		spec.GlobalCIDR = localCluster.Spec.GlobalCIDR
	} else {
		logger.Fatalf("Cluster %s is not configured to use globalCidr", spec.ClusterID)
	}

	gatewayMonitor, err := controllers.NewGatewayMonitor(spec, append(cidr.ExtractIPv4Subnets(localCluster.Spec.ClusterCIDR),
		cidr.ExtractIPv4Subnets(localCluster.Spec.ServiceCIDR)...),
		&watcher.Config{RestConfig: cfg})
	logger.FatalOnError(err, "Error creating gatewayMonitor")

	err = gatewayMonitor.Start()
	logger.FatalOnError(err, "Error starting the gatewayMonitor")

	<-stopCh
	gatewayMonitor.Stop()

	logger.Infof("All controllers stopped or exited. Stopping main loop")

	if err := httpServer.Shutdown(context.TODO()); err != nil {
		logger.Errorf(err, "Error shutting down metrics HTTP server")
	}
}

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}

func startHTTPServer(spec controllers.Specification) *http.Server {
	srv := &http.Server{Addr: ":" + spec.MetricsPort, ReadHeaderTimeout: 60 * time.Second}

	http.Handle("/metrics", promhttp.Handler())

	go func() {
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			logger.Errorf(err, "Error starting metrics server")
		}
	}()

	return srv
}
