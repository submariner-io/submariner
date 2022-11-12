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
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kelseyhightower/envconfig"
	extErrors "github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/watcher"
	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cableengine"
	"github.com/submariner-io/submariner/pkg/cableengine/healthchecker"
	"github.com/submariner-io/submariner/pkg/cableengine/syncer"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/controllers/datastoresyncer"
	"github.com/submariner-io/submariner/pkg/controllers/tunnel"
	"github.com/submariner-io/submariner/pkg/endpoint"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	"github.com/submariner-io/submariner/pkg/pod"
	"github.com/submariner-io/submariner/pkg/types"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

var (
	localMasterURL  string
	localKubeconfig string
)

func init() {
	flag.StringVar(&localKubeconfig, "kubeconfig", "", "Path to kubeconfig of local cluster. Only required if out-of-cluster.")
	flag.StringVar(&localMasterURL, "master", "",
		"The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
}

type leaderConfig struct {
	LeaseDuration int64
	RenewDeadline int64
	RetryPeriod   int64
}

type componentsType struct {
	restConfig         *rest.Config
	cableHealthChecker healthchecker.Interface
	cableEngineSyncer  *syncer.GatewaySyncer
	cableEngine        cableengine.Engine
	publicIPWatcher    *endpoint.PublicIPWatcher
	datastoreSyncer    *datastoresyncer.DatastoreSyncer
	gatewayPod         *pod.GatewayPod
	submSpec           types.SubmarinerSpecification
	stopCh             <-chan struct{}
}

const (
	leadershipConfigEnvPrefix = "leadership"
	defaultLeaseDuration      = 10 // In Seconds
	defaultRenewDeadline      = 5  // In Seconds
	defaultRetryPeriod        = 2  // In Seconds
)

var (
	VERSION = "not-compiled-properly"
	logger  = log.Logger{Logger: logf.Log.WithName("main")}
)

func main() {
	kzerolog.AddFlags(nil)
	flag.Parse()
	kzerolog.InitK8sLogging()

	logger.Info("Starting the submariner gateway engine")

	components := &componentsType{
		// set up signals so we handle the first shutdown signal gracefully
		stopCh: signals.SetupSignalHandler().Done(),
	}

	logger.FatalOnError(envconfig.Process("submariner", &components.submSpec), "Error processing env vars")

	logger.Info("Parsed env variables", components.submSpec)
	httpServer := startHTTPServer(&components.submSpec)

	var err error

	components.restConfig, err = clientcmd.BuildConfigFromFlags(localMasterURL, localKubeconfig)
	logger.FatalOnError(err, "Error building kubeconfig")

	submarinerClient, err := submarinerClientset.NewForConfig(components.restConfig)
	logger.FatalOnError(err, "Error creating submariner clientset")

	k8sClient, err := kubernetes.NewForConfig(components.restConfig)
	logger.FatalOnError(err, "Error creating Kubernetes clientset")

	logger.FatalOnError(subv1.AddToScheme(scheme.Scheme), "Error adding submariner types to the scheme")

	logger.Info("Creating the cable engine")

	localCluster := submarinerClusterFrom(&components.submSpec)

	if components.submSpec.CableDriver == "" {
		components.submSpec.CableDriver = cable.GetDefaultCableDriver()
	}

	components.submSpec.CableDriver = strings.ToLower(components.submSpec.CableDriver)

	airGapped := isAirGappedDeployment()
	logger.Infof("AIR_GAPPED_DEPLOYMENT is set to %t", airGapped)

	localEndpoint, err := endpoint.GetLocal(&components.submSpec, k8sClient, airGapped)
	logger.FatalOnError(err, "Error creating local endpoint object")

	components.cableEngine = cableengine.NewEngine(localCluster, localEndpoint)

	natDiscovery, err := natdiscovery.New(localEndpoint)
	logger.FatalOnError(err, "Error creating the NAT discovery handler")

	logger.Info("Creating the datastore syncer")

	components.datastoreSyncer = datastoresyncer.New(&broker.SyncerConfig{
		LocalRestConfig: components.restConfig,
		LocalNamespace:  components.submSpec.Namespace,
	}, localCluster, localEndpoint)

	components.initCableHealthChecker()

	components.cableEngineSyncer = syncer.NewGatewaySyncer(
		components.cableEngine,
		submarinerClient.SubmarinerV1().Gateways(components.submSpec.Namespace),
		VERSION, components.cableHealthChecker)

	if components.submSpec.Uninstall {
		logger.Info("Uninstalling the submariner gateway engine")

		components.uninstallGateway()

		return
	}

	components.cableEngine.SetupNATDiscovery(natDiscovery)

	logger.FatalOnError(natDiscovery.Run(components.stopCh), "Error starting NAT discovery server")

	components.gatewayPod, err = pod.NewGatewayPod(k8sClient)
	logger.FatalOnError(err, "Error creating a handler to update the gateway pod")

	components.cableEngineSyncer.Run(components.stopCh)

	if !airGapped {
		components.initPublicIPWatcher(k8sClient, submarinerClient, localEndpoint)
	}

	leClient, err := kubernetes.NewForConfig(rest.AddUserAgent(components.restConfig, "leader-election"))
	if err != nil {
		components.fatal("Error creating leader election kubernetes clientset: %s", err)
	}

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(logger.V(log.DEBUG).Infof)
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "submariner-controller"})

	go func() {
		if err = startLeaderElection(leClient, recorder, components.becameLeader, components.lostLeader); err != nil {
			components.fatal("Error starting leader election: %v", err)
		}
	}()

	<-components.stopCh

	if err := components.cableEngine.Cleanup(); err != nil {
		logger.Error(nil, "Error cleaning up cableEngine resources before removing Gateway")
	}

	logger.Info("All controllers stopped or exited. Stopping main loop")

	if err := httpServer.Shutdown(context.TODO()); err != nil {
		logger.Errorf(err, "Error shutting down metrics HTTP server")
	}
}

func isAirGappedDeployment() bool {
	return os.Getenv("AIR_GAPPED_DEPLOYMENT") == "true"
}

func (c *componentsType) becameLeader(context.Context) {
	if err := c.cableEngine.StartEngine(); err != nil {
		c.fatal("Error starting the cable engine: %v", err)
	}

	var wg sync.WaitGroup

	wg.Add(5)

	go func() {
		defer wg.Done()

		if err := c.gatewayPod.SetHALabels(subv1.HAStatusActive); err != nil {
			c.fatal("Error updating pod label: %s", err)
		}
	}()

	go func() {
		defer wg.Done()

		watcherConfig := &watcher.Config{RestConfig: c.restConfig}
		if err := tunnel.StartController(c.cableEngine, c.submSpec.Namespace, watcherConfig, c.stopCh); err != nil {
			c.fatal("Error running the tunnel controller: %v", err)
		}
	}()

	go func() {
		defer wg.Done()

		if err := c.datastoreSyncer.Start(c.stopCh); err != nil {
			c.fatal("Error running the datastore syncer: %v", err)
		}
	}()

	go func() {
		defer wg.Done()

		if c.cableHealthChecker != nil {
			if err := c.cableHealthChecker.Start(c.stopCh); err != nil {
				logger.Errorf(err, "Error starting healthChecker")
			}
		}
	}()

	go func() {
		defer wg.Done()

		if c.publicIPWatcher != nil {
			c.publicIPWatcher.Run(c.stopCh)
		}
	}()

	wg.Wait()
	<-c.stopCh
}

func (c *componentsType) lostLeader() {
	if err := c.gatewayPod.SetHALabels(subv1.HAStatusPassive); err != nil {
		logger.Warningf("Error updating pod label: %s", err)
	}

	c.cableEngineSyncer.CleanupGatewayEntry()
	logger.Fatalf("Leader election lost, shutting down")
}

func (c *componentsType) initCableHealthChecker() {
	var err error

	if !c.submSpec.HealthCheckEnabled {
		logger.Info("The CableEngine HealthChecker is disabled")
	} else {
		c.cableHealthChecker, err = healthchecker.New(&healthchecker.Config{
			WatcherConfig:      &watcher.Config{RestConfig: c.restConfig},
			EndpointNamespace:  c.submSpec.Namespace,
			ClusterID:          c.submSpec.ClusterID,
			PingInterval:       c.submSpec.HealthCheckInterval,
			MaxPacketLossCount: c.submSpec.HealthCheckMaxPacketLossCount,
		})
		if err != nil {
			logger.Errorf(err, "Error creating healthChecker")
		}
	}
}

func (c *componentsType) initPublicIPWatcher(k8sClient kubernetes.Interface, submarinerClient *submarinerClientset.Clientset,
	localEndpoint *types.SubmarinerEndpoint,
) {
	publicIPConfig := &endpoint.PublicIPWatcherConfig{
		SubmSpec:      &c.submSpec,
		K8sClient:     k8sClient,
		Endpoints:     submarinerClient.SubmarinerV1().Endpoints(c.submSpec.Namespace),
		LocalEndpoint: *localEndpoint,
	}

	c.publicIPWatcher = endpoint.NewPublicIPWatcher(publicIPConfig)
}

func submarinerClusterFrom(submSpec *types.SubmarinerSpecification) *types.SubmarinerCluster {
	return &types.SubmarinerCluster{
		ID: submSpec.ClusterID,
		Spec: subv1.ClusterSpec{
			ClusterID:   submSpec.ClusterID,
			ColorCodes:  []string{"blue"}, // This is a fake value, used only for upgrade purposes
			ServiceCIDR: submSpec.ServiceCidr,
			ClusterCIDR: submSpec.ClusterCidr,
			GlobalCIDR:  submSpec.GlobalCidr,
		},
	}
}

func startHTTPServer(spec *types.SubmarinerSpecification) *http.Server {
	srv := &http.Server{Addr: ":" + spec.MetricsPort, ReadHeaderTimeout: 60 * time.Second}

	http.Handle("/metrics", promhttp.Handler())

	go func() {
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			logger.Errorf(err, "Error starting metrics server")
		}
	}()

	return srv
}

func startLeaderElection(leaderElectionClient kubernetes.Interface, recorder resourcelock.EventRecorder,
	run func(ctx context.Context), end func(),
) error {
	gwLeadershipConfig := leaderConfig{}

	err := envconfig.Process(leadershipConfigEnvPrefix, &gwLeadershipConfig)
	if err != nil {
		return extErrors.Wrapf(err, "error processing environment config for %s", leadershipConfigEnvPrefix)
	}

	// Use default values when GatewayLeadership environment variables are not configured
	if gwLeadershipConfig.LeaseDuration == 0 {
		gwLeadershipConfig.LeaseDuration = defaultLeaseDuration
	}

	if gwLeadershipConfig.RenewDeadline == 0 {
		gwLeadershipConfig.RenewDeadline = defaultRenewDeadline
	}

	if gwLeadershipConfig.RetryPeriod == 0 {
		gwLeadershipConfig.RetryPeriod = defaultRetryPeriod
	}

	logger.Infof("Gateway leader election config values: %#v", gwLeadershipConfig)

	id, err := os.Hostname()
	if err != nil {
		return extErrors.Wrap(err, "error getting hostname")
	}

	kubeconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	)

	namespace, _, err := kubeconfig.Namespace()
	if err != nil {
		namespace = "submariner"
		logger.Infof("Could not obtain a namespace to use for the leader election lock - the error was: %v. Using the default %q namespace.",
			err, namespace)
	} else {
		logger.Infof("Using namespace %q for the leader election lock", namespace)
	}

	// Lock required for leader election
	rl, err := resourcelock.New(resourcelock.ConfigMapsLeasesResourceLock, namespace, "submariner-gateway-lock",
		leaderElectionClient.CoreV1(), leaderElectionClient.CoordinationV1(), resourcelock.ResourceLockConfig{
			Identity:      id + "-submariner-gateway",
			EventRecorder: recorder,
		})
	if err != nil {
		return extErrors.Wrap(err, "error creating leader election resource lock")
	}

	leaderelection.RunOrDie(context.TODO(), leaderelection.LeaderElectionConfig{
		Lock:          rl,
		LeaseDuration: time.Duration(gwLeadershipConfig.LeaseDuration) * time.Second,
		RenewDeadline: time.Duration(gwLeadershipConfig.RenewDeadline) * time.Second,
		RetryPeriod:   time.Duration(gwLeadershipConfig.RetryPeriod) * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: run,
			OnStoppedLeading: end,
		},
	})

	return nil
}

func (c *componentsType) fatal(format string, args ...interface{}) {
	err := fmt.Errorf(format, args...)
	c.cableEngineSyncer.SetGatewayStatusError(err)

	if err := c.gatewayPod.SetHALabels(subv1.HAStatusPassive); err != nil {
		logger.Warningf("Error updating pod label: %s", err)
	}

	logger.Fatal(err.Error())
}

func (c *componentsType) uninstallGateway() {
	err := c.cableEngine.StartEngine()
	if err != nil {
		// As we are in the process of cleaning up, ignore any initialization errors.
		logger.Errorf(err, "Error starting the cable driver")
	}

	// The Gateway object has to be deleted before invoking the cableEngine.Cleanup
	c.cableEngineSyncer.CleanupGatewayEntry()

	err = c.cableEngine.Cleanup()
	if err != nil {
		logger.Errorf(err, "Error while cleaning up of cable drivers")
	}

	dsErr := c.datastoreSyncer.Cleanup()

	logger.FatalOnError(dsErr, "Error cleaning up the datastore")
}
