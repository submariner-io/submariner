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
	"errors"
	"flag"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	"github.com/submariner-io/admiral/pkg/names"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/util"
	admversion "github.com/submariner-io/admiral/pkg/version"
	"github.com/submariner-io/admiral/pkg/watcher"
	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cableengine"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/gateway"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/versions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

var (
	localMasterURL  string
	localKubeconfig string
	showVersion     = false
	logger          = log.Logger{Logger: logf.Log.WithName("main")}
)

func init() {
	flag.StringVar(&localKubeconfig, "kubeconfig", "", "Path to kubeconfig of local cluster. Only required if out-of-cluster.")
	flag.StringVar(&localMasterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
	flag.BoolVar(&showVersion, "version", showVersion, "Show version")
}

type leaderConfig struct {
	LeaseDuration int64
	RenewDeadline int64
	RetryPeriod   int64
}

const leadershipConfigEnvPrefix = "leadership"

func main() {
	kzerolog.AddFlags(nil)
	flag.Parse()

	admversion.Print(names.GatewayComponent, versions.Submariner())

	if showVersion {
		return
	}

	kzerolog.InitK8sLogging()

	versions.Log(&logger)

	gwLeadershipConfig := leaderConfig{}
	logger.FatalfOnError(envconfig.Process(leadershipConfigEnvPrefix, &gwLeadershipConfig), "Error processing %s env vars",
		leadershipConfigEnvPrefix)

	submSpec := types.SubmarinerSpecification{}
	logger.FatalOnError(envconfig.Process("submariner", &submSpec), "Error processing env vars")

	logger.Infof("Parsed env variables: %#v", submSpec)
	httpServer := startHTTPServer(&submSpec)

	var err error

	util.AddCertificateErrorHandler(submSpec.HaltOnCertError)

	restConfig, err := clientcmd.BuildConfigFromFlags(localMasterURL, localKubeconfig)
	logger.FatalOnError(err, "Error building kubeconfig")

	submarinerClient, err := submarinerClientset.NewForConfig(restConfig)
	logger.FatalOnError(err, "Error creating submariner clientset")

	k8sClient, err := kubernetes.NewForConfig(restConfig)
	logger.FatalOnError(err, "Error creating Kubernetes clientset")

	leClient, err := kubernetes.NewForConfig(rest.AddUserAgent(restConfig, "leader-election"))
	logger.FatalOnError(err, "Error creating leader election kubernetes clientset")

	logger.FatalOnError(subv1.AddToScheme(scheme.Scheme), "Error adding submariner types to the scheme")

	gw, err := gateway.New(&gateway.Config{
		LeaderElectionConfig: gateway.LeaderElectionConfig{
			LeaseDuration: time.Duration(gwLeadershipConfig.LeaseDuration) * time.Second,
			RenewDeadline: time.Duration(gwLeadershipConfig.RenewDeadline) * time.Second,
			RetryPeriod:   time.Duration(gwLeadershipConfig.RetryPeriod) * time.Second,
		},
		Spec: submSpec,
		SyncerConfig: broker.SyncerConfig{
			LocalRestConfig: restConfig,
		},
		WatcherConfig: watcher.Config{
			RestConfig: restConfig,
		},
		SubmarinerClient:     submarinerClient,
		KubeClient:           k8sClient,
		LeaderElectionClient: leClient,
		NewCableEngine:       cableengine.NewEngine,
		NewNATDiscovery:      natdiscovery.New,
	})
	logger.FatalOnError(err, "Error creating gateway instance")

	err = gw.Run(signals.SetupSignalHandler().Done())

	if err := httpServer.Shutdown(context.Background()); err != nil {
		logger.Errorf(err, "Error shutting down metrics HTTP server")
	}

	logger.FatalOnError(err, "Error running the gateway")
}

func startHTTPServer(spec *types.SubmarinerSpecification) *http.Server {
	srv := &http.Server{Addr: ":" + spec.MetricsPort, ReadHeaderTimeout: 60 * time.Second}

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/debug", pprof.Profile)

	go func() {
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			logger.Errorf(err, "Error starting metrics server")
		}
	}()

	return srv
}
