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
	"k8s.io/klog"
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

const (
	leadershipConfigEnvPrefix = "leadership"
	defaultLeaseDuration      = 10 // In Seconds
	defaultRenewDeadline      = 5  // In Seconds
	defaultRetryPeriod        = 2  // In Seconds
)

var VERSION = "not-compiled-properly"

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	klog.Info("Starting the submariner gateway engine")

	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler().Done()

	httpServer := startHTTPServer()

	var submSpec types.SubmarinerSpecification

	fatalOnErr(envconfig.Process("submariner", &submSpec), "Error processing env vars")

	cfg, err := clientcmd.BuildConfigFromFlags(localMasterURL, localKubeconfig)
	fatalOnErr(err, "Error building kubeconfig")

	submarinerClient, err := submarinerClientset.NewForConfig(cfg)
	fatalOnErr(err, "Error creating submariner clientset")

	k8sClient, err := kubernetes.NewForConfig(cfg)
	fatalOnErr(err, "Error creating Kubernetes clientset")

	fatalOnErr(subv1.AddToScheme(scheme.Scheme), "Error adding submariner types to the scheme")

	klog.Info("Creating the cable engine")

	localCluster := submarinerClusterFrom(&submSpec)

	if submSpec.CableDriver == "" {
		submSpec.CableDriver = cable.GetDefaultCableDriver()
	}

	submSpec.CableDriver = strings.ToLower(submSpec.CableDriver)

	localEndpoint, err := endpoint.GetLocal(&submSpec, k8sClient)
	fatalOnErr(err, "Error creating local endpoint object")

	cableEngine := cableengine.NewEngine(localCluster, localEndpoint)
	natDiscovery, err := natdiscovery.New(localEndpoint)
	fatalOnErr(err, "Error creating the NAT discovery handler")

	klog.Info("Creating the datastore syncer")

	dsSyncer := datastoresyncer.New(&broker.SyncerConfig{
		LocalRestConfig: cfg,
		LocalNamespace:  submSpec.Namespace,
	}, localCluster, localEndpoint)

	cableHealthchecker := getCableHealthChecker(cfg, &submSpec)

	cableEngineSyncer := syncer.NewGatewaySyncer(
		cableEngine,
		submarinerClient.SubmarinerV1().Gateways(submSpec.Namespace),
		VERSION, cableHealthchecker)

	if submSpec.Uninstall {
		klog.Info("Uninstalling the submariner gateway engine")

		uninstallGateway(cableEngine, cableEngineSyncer, dsSyncer)

		return
	}

	cableEngine.SetupNATDiscovery(natDiscovery)

	fatalOnErr(natDiscovery.Run(stopCh), "Error starting NAT discovery server")

	gwPod, err := pod.NewGatewayPod(k8sClient)
	fatalOnErr(err, "Error creating a handler to update the gateway pod")

	cleanup := cleanupHandler{
		gatewayPod: gwPod,
		gwSyncer:   cableEngineSyncer,
	}

	cableEngineSyncer.Run(stopCh)

	publicIPWatcher := getPublicIPWatcher(&submSpec, k8sClient, submarinerClient, localEndpoint)

	becameLeader := func(context.Context) {
		if err = cableEngine.StartEngine(); err != nil {
			cleanup.fatal("Error starting the cable engine: %v", err)
		}

		var wg sync.WaitGroup

		wg.Add(5)

		go func() {
			defer wg.Done()

			if err := gwPod.SetHALabels(subv1.HAStatusActive); err != nil {
				cleanup.fatal("Error updating pod label: %s", err)
			}
		}()

		go func() {
			defer wg.Done()

			if err = tunnel.StartController(cableEngine, submSpec.Namespace, &watcher.Config{RestConfig: cfg}, stopCh); err != nil {
				cleanup.fatal("Error running the tunnel controller: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			if err = dsSyncer.Start(stopCh); err != nil {
				cleanup.fatal("Error running the datastore syncer: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			if cableHealthchecker != nil {
				if err = cableHealthchecker.Start(stopCh); err != nil {
					klog.Errorf("Error starting healthChecker: %v", err)
				}
			}
		}()

		go func() {
			defer wg.Done()

			if publicIPWatcher != nil {
				publicIPWatcher.Run(stopCh)
			}
		}()

		wg.Wait()
		<-stopCh
	}

	leClient, err := kubernetes.NewForConfig(rest.AddUserAgent(cfg, "leader-election"))
	if err != nil {
		cleanup.fatal("Error creating leader election kubernetes clientset: %s", err)
	}

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.V(log.DEBUG).Infof)
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "submariner-controller"})

	lostLeader := func() {
		if err := gwPod.SetHALabels(subv1.HAStatusPassive); err != nil {
			klog.Warningf("Error updating pod label: %s", err)
		}

		cableEngineSyncer.CleanupGatewayEntry()
		klog.Fatalf("Leader election lost, shutting down")
	}

	go func() {
		if err = startLeaderElection(leClient, recorder, becameLeader, lostLeader); err != nil {
			cleanup.fatal("Error starting leader election: %v", err)
		}
	}()

	<-stopCh

	if err := cableEngine.Cleanup(); err != nil {
		klog.Errorf("error cleaning up cableEngine resources before removing Gateway")
	}

	klog.Info("All controllers stopped or exited. Stopping main loop")

	if err := httpServer.Shutdown(context.TODO()); err != nil {
		klog.Errorf("Error shutting down metrics HTTP server: %v", err)
	}
}

func getCableHealthChecker(cfg *rest.Config, submSpec *types.SubmarinerSpecification) healthchecker.Interface {
	var cableHealthchecker healthchecker.Interface
	var err error

	if !submSpec.HealthCheckEnabled {
		klog.Info("The CableEngine HealthChecker is disabled")
	} else {
		cableHealthchecker, err = healthchecker.New(&healthchecker.Config{
			WatcherConfig:      &watcher.Config{RestConfig: cfg},
			EndpointNamespace:  submSpec.Namespace,
			ClusterID:          submSpec.ClusterID,
			PingInterval:       submSpec.HealthCheckInterval,
			MaxPacketLossCount: submSpec.HealthCheckMaxPacketLossCount,
		})
		if err != nil {
			klog.Errorf("Error creating healthChecker: %v", err)
		}
	}

	return cableHealthchecker
}

func getPublicIPWatcher(submSpec *types.SubmarinerSpecification,
	k8sClient kubernetes.Interface, submarinerClient *submarinerClientset.Clientset,
	localEndpoint *types.SubmarinerEndpoint,
) *endpoint.PublicIPWatcher {
	publicIPConfig := &endpoint.PublicIPWatcherConfig{
		SubmSpec:      submSpec,
		K8sClient:     k8sClient,
		Endpoints:     submarinerClient.SubmarinerV1().Endpoints(submSpec.Namespace),
		LocalEndpoint: *localEndpoint,
	}

	publicIPWatcher := endpoint.NewPublicIPWatcher(publicIPConfig)

	return publicIPWatcher
}

func fatalOnErr(err error, msg string) {
	if err == nil {
		return
	}

	klog.Fatalf("%s: %+v", msg, err)
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

func startHTTPServer() *http.Server {
	srv := &http.Server{Addr: ":8080", ReadHeaderTimeout: 60 * time.Second}

	http.Handle("/metrics", promhttp.Handler())

	go func() {
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			klog.Errorf("Error starting metrics server: %v", err)
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

	klog.Infof("Gateway leader election config values: %#v", gwLeadershipConfig)

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
		klog.Infof("Could not obtain a namespace to use for the leader election lock - the error was: %v. Using the default %q namespace.",
			namespace, err)
	} else {
		klog.Infof("Using namespace %q for the leader election lock", namespace)
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

type cleanupHandler struct {
	gatewayPod pod.GatewayPodInterface
	gwSyncer   *syncer.GatewaySyncer
}

func (c *cleanupHandler) fatal(format string, args ...interface{}) {
	err := fmt.Errorf(format, args...)
	c.gwSyncer.SetGatewayStatusError(err)

	if err := c.gatewayPod.SetHALabels(subv1.HAStatusPassive); err != nil {
		klog.Warningf("Error updating pod label: %s", err)
	}

	klog.Fatal(err.Error())
}

func uninstallGateway(cableEngine cableengine.Engine, cableEngineSyncer *syncer.GatewaySyncer,
	dsSyncer *datastoresyncer.DatastoreSyncer,
) {
	err := cableEngine.StartEngine()
	if err != nil {
		// As we are in the process of cleaning up, ignore any initialization errors.
		klog.Errorf("Error starting the cable driver: %v", err)
	}

	// The Gateway object has to be deleted before invoking the cableEngine.Cleanup
	cableEngineSyncer.CleanupGatewayEntry()

	err = cableEngine.Cleanup()
	if err != nil {
		klog.Errorf("Error while cleaning up of cable drivers: %v", err)
	}

	dsErr := dsSyncer.Cleanup()

	fatalOnErr(dsErr, "Error cleaning up the datastore")
}
