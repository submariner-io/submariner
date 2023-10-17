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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	defaultLeaseDuration = 10 * time.Second
	defaultRenewDeadline = 5 * time.Second
	defaultRetryPeriod   = 2 * time.Second
)

type Interface interface {
	Run(<-chan struct{}) error
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
	airGapped          bool
	cableHealthChecker healthchecker.Interface
	cableEngineSyncer  *syncer.GatewaySyncer
	cableEngine        cableengine.Engine
	publicIPWatcher    *endpoint.PublicIPWatcher
	datastoreSyncer    *datastoresyncer.DatastoreSyncer
	natDiscovery       natdiscovery.Interface
	gatewayPod         *pod.GatewayPod
	hostName           string
	localEndpoint      *types.SubmarinerEndpoint
	stopCh             <-chan struct{}
	fatalError         chan error
	waitGroup          sync.WaitGroup
	recorder           record.EventRecorder
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

func (g *gatewayType) Run(stopCh <-chan struct{}) error {
	if g.Spec.Uninstall {
		logger.Info("Uninstalling the submariner gateway engine")

		return g.uninstall()
	}

	logger.Info("Starting the gateway engine")

	g.stopCh = stopCh

	g.cableEngine.SetupNATDiscovery(g.natDiscovery)

	err := g.natDiscovery.Run(g.stopCh)
	if err != nil {
		return errors.Wrap(err, "error starting NAT discovery server")
	}

	g.gatewayPod, err = pod.NewGatewayPod(g.KubeClient)
	if err != nil {
		return errors.Wrap(err, "error creating a handler to update the gateway pod")
	}

	g.runAsync(g.cableEngineSyncer.Run)

	if !g.airGapped {
		g.initPublicIPWatcher()
	}

	err = g.startLeaderElection()
	if err != nil {
		return errors.Wrap(err, "error starting leader election")
	}

	select {
	case <-g.stopCh:
	case fatalErr := <-g.fatalError:
		g.cableEngineSyncer.SetGatewayStatusError(fatalErr)

		if err := g.gatewayPod.SetHALabels(subv1.HAStatusPassive); err != nil {
			logger.Warningf("Error updating pod label: %s", err)
		}

		return fatalErr
	}

	g.waitGroup.Wait()

	logger.Info("Gateway engine stopped")

	return nil
}

func (g *gatewayType) runAsync(run func(<-chan struct{})) {
	g.waitGroup.Add(1)

	go func() {
		defer g.waitGroup.Done()
		run(g.stopCh)
	}()
}

func (g *gatewayType) startLeaderElection() error {
	rl, err := resourcelock.New(resourcelock.LeasesResourceLock, g.Spec.Namespace, "submariner-gateway-lock",
		g.LeaderElectionClient.CoreV1(), g.LeaderElectionClient.CoordinationV1(), resourcelock.ResourceLockConfig{
			Identity:      g.hostName + "-submariner-gateway",
			EventRecorder: g.recorder,
		})
	if err != nil {
		return errors.Wrap(err, "error creating leader election resource lock")
	}

	go leaderelection.RunOrDie(context.Background(), leaderelection.LeaderElectionConfig{
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

func (g *gatewayType) onStartedLeading(_ context.Context) {
	if err := g.cableEngine.StartEngine(); err != nil {
		g.fatalError <- errors.Wrap(err, "error starting the cable engine")
		return
	}

	go func() {
		if err := g.gatewayPod.SetHALabels(subv1.HAStatusActive); err != nil {
			g.fatalError <- errors.Wrap(err, "error updating pod label")
		}
	}()

	go func() {
		watcherConfig := g.WatcherConfig
		if err := tunnel.StartController(g.cableEngine, g.Spec.Namespace, &watcherConfig, g.stopCh); err != nil {
			g.fatalError <- errors.Wrap(err, "error running the tunnel controller")
		}
	}()

	go func() {
		if err := g.datastoreSyncer.Start(g.stopCh); err != nil {
			g.fatalError <- errors.Wrap(err, "error running the datastore syncer")
		}
	}()

	if g.cableHealthChecker != nil {
		go func() {
			if err := g.cableHealthChecker.Start(g.stopCh); err != nil {
				logger.Errorf(err, "Error starting healthChecker")
			}
		}()
	}

	if g.publicIPWatcher != nil {
		go func() {
			g.publicIPWatcher.Run(g.stopCh)
		}()
	}
}

func (g *gatewayType) onStoppedLeading() {
	g.cableEngineSyncer.CleanupGatewayEntry()

	g.fatalError <- errors.New("leader election lost, shutting down")
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

func (g *gatewayType) uninstall() error {
	err := g.cableEngine.StartEngine()
	if err != nil {
		// As we are in the process of cleaning up, ignore any initialization errors.
		logger.Errorf(err, "Error starting the cable driver")
	}

	// The Gateway object has to be deleted before invoking the cableEngine.Cleanup
	g.cableEngineSyncer.CleanupGatewayEntry()

	err = g.cableEngine.Cleanup()
	if err != nil {
		logger.Errorf(err, "Error while cleaning up of cable drivers")
	}

	dsErr := g.datastoreSyncer.Cleanup()

	return errors.Wrap(dsErr, "Error cleaning up the datastore")
}

func submarinerClusterFrom(submSpec *types.SubmarinerSpecification) *types.SubmarinerCluster {
	return &types.SubmarinerCluster{
		ID: submSpec.ClusterID,
		Spec: subv1.ClusterSpec{
			ClusterID:   submSpec.ClusterID,
			ColorCodes:  []string{"blue"}, // This is a fake value, used only for upgrade purposes
			ServiceCIDR: cidr.ExtractIPv4Subnets(submSpec.ServiceCidr),
			ClusterCIDR: cidr.ExtractIPv4Subnets(submSpec.ClusterCidr),
			GlobalCIDR:  submSpec.GlobalCidr,
		},
	}
}
