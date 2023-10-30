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

package gateway

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/watcher"
	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cableengine"
	"github.com/submariner-io/submariner/pkg/cableengine/healthchecker"
	"github.com/submariner-io/submariner/pkg/cableengine/syncer"
	"github.com/submariner-io/submariner/pkg/cidr"
	submclientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/controllers/datastoresyncer"
	"github.com/submariner-io/submariner/pkg/controllers/tunnel"
	"github.com/submariner-io/submariner/pkg/endpoint"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	"github.com/submariner-io/submariner/pkg/pod"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/versions"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	defaultLeaseDuration = 15 * time.Second
	defaultRenewDeadline = 10 * time.Second
	defaultRetryPeriod   = 2 * time.Second

	LeaderElectionLockName = "submariner-gateway-lock"
)

type Interface interface {
	Run(ctx context.Context) error
}

type LeaderElectionConfig struct {
	LeaseDuration time.Duration
	RenewDeadline time.Duration
	RetryPeriod   time.Duration
}

type Config struct {
	LeaderElectionConfig
	Spec                 types.SubmarinerSpecification
	SyncerConfig         broker.SyncerConfig
	WatcherConfig        watcher.Config
	SubmarinerClient     submclientset.Interface
	KubeClient           kubernetes.Interface
	LeaderElectionClient kubernetes.Interface
	NewCableEngine       func(localCluster *types.SubmarinerCluster, localEndpoint *types.SubmarinerEndpoint) cableengine.Engine
	NewNATDiscovery      func(localEndpoint *types.SubmarinerEndpoint) (natdiscovery.Interface, error)
}

type gatewayType struct {
	Config
	airGapped               bool
	cableHealthChecker      healthchecker.Interface
	cableEngineSyncer       *syncer.GatewaySyncer
	cableEngine             cableengine.Engine
	publicIPWatcher         *endpoint.PublicIPWatcher
	datastoreSyncer         *datastoresyncer.DatastoreSyncer
	natDiscovery            natdiscovery.Interface
	gatewayPod              *pod.GatewayPod
	hostName                string
	localEndpoint           *types.SubmarinerEndpoint
	fatalError              chan error
	leaderComponentsStarted *sync.WaitGroup
	recorder                record.EventRecorder
}

var logger = log.Logger{Logger: logf.Log.WithName("Gateway")}

func New(config *Config) (Interface, error) {
	logger.Info("Initializing the gateway engine")

	g := &gatewayType{
		Config:     *config,
		fatalError: make(chan error),
	}

	if g.LeaseDuration == 0 {
		g.LeaseDuration = defaultLeaseDuration
	}

	if g.RenewDeadline == 0 {
		g.RenewDeadline = defaultRenewDeadline
	}

	if g.RetryPeriod == 0 {
		g.RetryPeriod = defaultRetryPeriod
	}

	var err error

	g.hostName, err = os.Hostname()
	if err != nil {
		return nil, errors.Wrap(err, "error getting hostname")
	}

	logger.Info("Creating the cable engine")

	localCluster := submarinerClusterFrom(&g.Spec)

	if g.Spec.CableDriver == "" {
		g.Spec.CableDriver = cable.GetDefaultCableDriver()
	}

	g.Spec.CableDriver = strings.ToLower(g.Spec.CableDriver)

	g.airGapped = os.Getenv("AIR_GAPPED_DEPLOYMENT") == "true"
	logger.Infof("AIR_GAPPED_DEPLOYMENT is set to %t", g.airGapped)

	g.localEndpoint, err = endpoint.GetLocal(&g.Spec, g.KubeClient, g.airGapped)
	if err != nil {
		return nil, errors.Wrap(err, "error creating local endpoint object")
	}

	g.cableEngine = g.NewCableEngine(localCluster, g.localEndpoint)

	g.natDiscovery, err = g.NewNATDiscovery(g.localEndpoint)
	if err != nil {
		return nil, errors.Wrap(err, "error creating the NAT discovery handler")
	}

	logger.Info("Creating the datastore syncer")

	g.SyncerConfig.LocalNamespace = g.Spec.Namespace

	g.datastoreSyncer = datastoresyncer.New(&g.SyncerConfig, localCluster, g.localEndpoint)

	g.initCableHealthChecker()

	g.cableEngineSyncer = syncer.NewGatewaySyncer(
		g.cableEngine,
		g.SubmarinerClient.SubmarinerV1().Gateways(g.Spec.Namespace),
		versions.Submariner(), g.cableHealthChecker)

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(logger.V(log.DEBUG).Infof)
	g.recorder = eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "submariner-controller"})

	return g, nil
}

func (g *gatewayType) Run(ctx context.Context) error {
	if g.Spec.Uninstall {
		logger.Info("Uninstalling the submariner gateway engine")

		return g.uninstall(ctx)
	}

	logger.Info("Starting the gateway engine")

	g.cableEngine.SetupNATDiscovery(g.natDiscovery)

	err := g.natDiscovery.Run(ctx.Done())
	if err != nil {
		return errors.Wrap(err, "error starting NAT discovery server")
	}

	g.gatewayPod, err = pod.NewGatewayPod(ctx, g.KubeClient)
	if err != nil {
		return errors.Wrap(err, "error creating a handler to update the gateway pod")
	}

	var waitGroup sync.WaitGroup

	g.runAsync(&waitGroup, func() {
		//nolint:contextcheck // Intentionally not passing the context b/c it can't be used after cancellation.
		g.cableEngineSyncer.Run(ctx.Done())
	})

	if !g.airGapped {
		g.initPublicIPWatcher()
	}

	err = g.startLeaderElection(ctx)
	if err != nil {
		return errors.Wrap(err, "error starting leader election")
	}

	select {
	case <-ctx.Done():
	case fatalErr := <-g.fatalError:
		g.cableEngineSyncer.SetGatewayStatusError(ctx, fatalErr)

		if err := g.gatewayPod.SetHALabels(ctx, subv1.HAStatusPassive); err != nil {
			logger.Warningf("Error updating pod label: %s", err)
		}

		return fatalErr
	}

	waitGroup.Wait()

	logger.Info("Gateway engine stopped")

	return nil
}

func (g *gatewayType) runAsync(waitGroup *sync.WaitGroup, run func()) {
	waitGroup.Add(1)

	go func() {
		defer waitGroup.Done()
		run()
	}()
}

func (g *gatewayType) startLeaderElection(ctx context.Context) error {
	logger.Info("Starting leader election")

	g.leaderComponentsStarted = &sync.WaitGroup{}

	rl, err := resourcelock.New(resourcelock.LeasesResourceLock, g.Spec.Namespace, LeaderElectionLockName,
		g.LeaderElectionClient.CoreV1(), g.LeaderElectionClient.CoordinationV1(), resourcelock.ResourceLockConfig{
			Identity:      g.hostName + "-submariner-gateway",
			EventRecorder: g.recorder,
		})
	if err != nil {
		return errors.Wrap(err, "error creating leader election resource lock")
	}

	go leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:          rl,
		LeaseDuration: g.LeaseDuration,
		RenewDeadline: g.RenewDeadline,
		RetryPeriod:   g.RetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: g.onStartedLeading,
			OnStoppedLeading: g.onStoppedLeading,
		},
	})

	return nil
}

func (g *gatewayType) onStartedLeading(ctx context.Context) {
	logger.Info("Leadership acquired - starting controllers")

	if err := g.cableEngine.StartEngine(); err != nil {
		g.fatalError <- errors.Wrap(err, "error starting the cable engine")
		return
	}

	g.runAsync(g.leaderComponentsStarted, func() {
		msgLogged := atomic.Bool{}

		_ = wait.PollUntilContextCancel(ctx, 100*time.Millisecond, true, func(ctx context.Context) (bool, error) {
			if err := g.gatewayPod.SetHALabels(ctx, subv1.HAStatusActive); err != nil {
				if msgLogged.CompareAndSwap(false, true) {
					logger.Warningf("Error updating pod label to active: %s", err)
				}

				return false, nil
			}

			return true, nil
		})
	})

	g.runAsync(g.leaderComponentsStarted, func() {
		err := g.datastoreSyncer.Start(ctx)
		if errors.Is(err, context.Canceled) {
			return
		}

		if err != nil {
			g.fatalError <- errors.Wrap(err, "error running the datastore syncer")
		}
	})

	g.runAsync(g.leaderComponentsStarted, func() {
		watcherConfig := g.WatcherConfig
		if err := tunnel.StartController(g.cableEngine, g.Spec.Namespace, &watcherConfig, ctx.Done()); err != nil {
			g.fatalError <- errors.Wrap(err, "error running the tunnel controller")
		}
	})

	if g.cableHealthChecker != nil {
		g.runAsync(g.leaderComponentsStarted, func() {
			if err := g.cableHealthChecker.Start(ctx.Done()); err != nil {
				logger.Errorf(err, "Error starting healthChecker")
			}
		})
	}

	if g.publicIPWatcher != nil {
		go g.publicIPWatcher.Run(ctx.Done())
	}
}

func (g *gatewayType) onStoppedLeading() {
	logger.Info("Leadership lost")

	// Make sure all the components were at least started before we try to restart.
	g.leaderComponentsStarted.Wait()

	if g.cableHealthChecker != nil {
		g.cableHealthChecker.Stop()
	}

	g.cableEngine.Stop()

	logger.Info("Controllers stopped")

	if err := g.gatewayPod.SetHALabels(context.Background(), subv1.HAStatusPassive); err != nil {
		logger.Warningf("Error updating pod label to passive: %s", err)
	}

	err := g.startLeaderElection(context.Background())
	if err != nil {
		g.fatalError <- errors.Wrap(err, "error restarting leader election")
	}
}

func (g *gatewayType) initPublicIPWatcher() {
	publicIPConfig := &endpoint.PublicIPWatcherConfig{
		SubmSpec:      &g.Spec,
		K8sClient:     g.KubeClient,
		Endpoints:     g.SubmarinerClient.SubmarinerV1().Endpoints(g.Spec.Namespace),
		LocalEndpoint: *g.localEndpoint,
	}

	g.publicIPWatcher = endpoint.NewPublicIPWatcher(publicIPConfig)
}

func (g *gatewayType) initCableHealthChecker() {
	var err error

	if !g.Spec.HealthCheckEnabled {
		logger.Info("The CableEngine HealthChecker is disabled")
	} else {
		watcherConfig := g.WatcherConfig
		g.cableHealthChecker, err = healthchecker.New(&healthchecker.Config{
			WatcherConfig:      &watcherConfig,
			EndpointNamespace:  g.Spec.Namespace,
			ClusterID:          g.Spec.ClusterID,
			PingInterval:       g.Spec.HealthCheckInterval,
			MaxPacketLossCount: g.Spec.HealthCheckMaxPacketLossCount,
		})
		if err != nil {
			logger.Errorf(err, "Error creating healthChecker")
		}
	}
}

func (g *gatewayType) uninstall(ctx context.Context) error {
	err := g.cableEngine.StartEngine()
	if err != nil {
		// As we are in the process of cleaning up, ignore any initialization errors.
		logger.Errorf(err, "Error starting the cable driver")
	}

	// The Gateway object has to be deleted before invoking the cableEngine.Cleanup
	g.cableEngineSyncer.CleanupGatewayEntry(ctx)

	err = g.cableEngine.Cleanup()
	if err != nil {
		logger.Errorf(err, "Error while cleaning up of cable drivers")
	}

	dsErr := g.datastoreSyncer.Cleanup(ctx)

	return errors.Wrap(dsErr, "Error cleaning up the datastore")
}

func submarinerClusterFrom(submSpec *types.SubmarinerSpecification) *types.SubmarinerCluster {
	// The Cluster resource requires a value for the GlobalCIDR.
	globalCIDR := submSpec.GlobalCidr
	if globalCIDR == nil {
		globalCIDR = []string{}
	}

	return &types.SubmarinerCluster{
		ID: submSpec.ClusterID,
		Spec: subv1.ClusterSpec{
			ClusterID:   submSpec.ClusterID,
			ColorCodes:  []string{"blue"}, // This is a fake value, used only for upgrade purposes
			ServiceCIDR: cidr.ExtractIPv4Subnets(submSpec.ServiceCidr),
			ClusterCIDR: cidr.ExtractIPv4Subnets(submSpec.ClusterCidr),
			GlobalCIDR:  globalCIDR,
		},
	}
}
