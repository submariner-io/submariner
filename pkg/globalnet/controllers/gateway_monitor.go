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

package controllers

import (
	"context"
	"os"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cidr"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/event/controller"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/ipam"
	"github.com/submariner-io/submariner/pkg/netlink"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	routeAgent "github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	k8snet "k8s.io/utils/net"
	"k8s.io/utils/set"
)

func NewGatewayMonitor(config *GatewayMonitorConfig) (Interface, error) {
	// We'll panic if config is nil, this is intentional
	gatewayMonitor := &gatewayMonitor{
		baseController:          newBaseController(),
		GatewayMonitorConfig:    *config,
		shuttingDown:            atomic.Bool{},
		remoteEndpointTimeStamp: map[string]metav1.Time{},
		leaderElectionInfo:      atomic.Pointer[LeaderElectionInfo]{},
	}

	// When transitioning to a non-gateway or shutting down, the GatewayMonitor cancels leader election which causes it to release the lock,
	// which clears the HolderIdentity field and sets LeaseDuration to one second. This enables the next instance to quickly acquire the
	// lock. However, if the controller instance crashes and doesn't properly release the lock, the next instance will have to await the
	// LeaseDuration period before acquiring the lock. So we don't want LeaseDuration set too high, and we don't want it too low either to
	// give the current instance enough time to complete stopping its controllers.
	//
	// The K8s leader election functionality periodically renews the lease. If it can't be renewed prior to the RenewDeadline, it stops.
	// For our usage, we don't have instances concurrently vying for leadership, so we really don't need to keep renewing the lease. Ideally
	// we would set the RenewDeadline very high to essentially disable it, but it needs to be less than the LeaseDuration setting which we
	// don't want too high.

	gatewayMonitor.LocalCIDRs = set.New(config.LocalCIDRs...).UnsortedList()

	if gatewayMonitor.LeaseDuration == 0 {
		gatewayMonitor.LeaseDuration = 20 * time.Second
	}

	if gatewayMonitor.RenewDeadline == 0 {
		gatewayMonitor.RenewDeadline = 15 * time.Second
	}

	if gatewayMonitor.RetryPeriod == 0 {
		gatewayMonitor.RetryPeriod = 2 * time.Second
	}

	gatewayMonitor.leaderElectionInfo.Store(&LeaderElectionInfo{})

	var err error

	gatewayMonitor.pFilter, err = packetfilter.New()
	if err != nil {
		return nil, errors.Wrap(err, "error creating packetfilter")
	}

	nodeName, ok := os.LookupEnv("NODE_NAME")
	if !ok {
		return nil, errors.New("error reading the NODE_NAME from the environment")
	}

	gatewayMonitor.nodeName = nodeName

	gatewayMonitor.syncerConfig = &syncer.ResourceSyncerConfig{
		SourceClient:    config.Client,
		SourceNamespace: corev1.NamespaceAll,
		Direction:       syncer.RemoteToLocal,
		RestMapper:      config.RestMapper,
		Federator:       broker.NewFederator(config.Client, config.RestMapper, corev1.NamespaceAll, ""),
		Scheme:          config.Scheme,
	}

	return &gatewayMonitorInterface{monitor: gatewayMonitor}, nil
}

func (g *gatewayMonitorInterface) Start() error {
	logger.Info("Starting GatewayMonitor to monitor the active Gateway node in the cluster.")

	registry, err := event.NewRegistry("globalnet-registry", event.AnyNetworkPlugin, g.monitor)
	if err != nil {
		return errors.Wrap(err, "error creating event registry")
	}

	eventController, err := controller.New(&controller.Config{
		Registry:   registry,
		RestMapper: g.monitor.RestMapper,
		Client:     g.monitor.Client,
		Scheme:     g.monitor.Scheme,
	})
	if err != nil {
		return errors.Wrap(err, "error starting the event controller")
	}

	return eventController.Start(g.monitor.stopCh) //nolint // No need to wrap
}

func (g *gatewayMonitorInterface) Stop() {
	_ = g.monitor.Stop()
}

func (g *gatewayMonitor) GetName() string {
	return "GatewayMonitor"
}

func (g *gatewayMonitor) GetNetworkPlugins() []string {
	return []string{event.AnyNetworkPlugin}
}

func (g *gatewayMonitor) Init() error {
	return g.createNATChain(constants.SmGlobalnetMarkChain)
}

func (g *gatewayMonitor) Stop() error {
	logger.Info("GatewayMonitor stopping")

	g.shuttingDown.Store(true)

	g.baseController.Stop()

	// stopControllers should be pretty quick but put a deadline on it, so we don't block shutdown for a long time.

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	g.stopControllers(ctx, true)

	return nil
}

func (g *gatewayMonitor) TransitionToGateway() error {
	remoteEndpoints := g.State().GetRemoteEndpoints()
	for i := range remoteEndpoints {
		g.markRemoteClusterTraffic(AddRules, remoteEndpoints[i].Spec.Subnets...)
	}

	configureTCPMTUProbe()

	g.startLeaderElection()

	return nil
}

func (g *gatewayMonitor) TransitionToNonGateway() error {
	g.stopControllers(context.Background(), true)
	return nil
}

func (g *gatewayMonitor) RemoteEndpointCreated(endpoint *v1.Endpoint) error {
	lastProcessedTime, ok := g.remoteEndpointTimeStamp[endpoint.Spec.ClusterID]

	if ok && lastProcessedTime.After(endpoint.CreationTimestamp.Time) {
		logger.Infof("Ignoring new remote %#v since a later endpoint was already processed", endpoint)
		return nil
	}

	logger.V(log.DEBUG).Infof("Endpoint %q, host: %q belongs to a remote cluster",
		endpoint.Spec.ClusterID, endpoint.Spec.Hostname)

	globalCIDR := g.GatewayMonitorConfig.Spec.GlobalCIDR[0]

	overlap, err := cidr.IsOverlapping(endpoint.Spec.Subnets, globalCIDR)
	if err != nil {
		// Ideally this case will never hit, as the subnets are valid CIDRs
		logger.Warningf("unable to validate overlapping Service CIDR: %s", err)
	}

	if overlap {
		// When GlobalNet is used, globalCIDRs allocated to the clusters should not overlap.
		// If they overlap, skip the endpoint as its an invalid configuration which is not supported.
		logger.Errorf(nil, "GlobalCIDR %q of local cluster %q overlaps with remote cluster %s",
			globalCIDR, g.GatewayMonitorConfig.Spec.ClusterID, endpoint.Spec.ClusterID)

		return nil
	}

	g.markRemoteClusterTraffic(AddRules, endpoint.Spec.Subnets...)

	g.remoteEndpointTimeStamp[endpoint.Spec.ClusterID] = endpoint.CreationTimestamp

	return nil
}

func (g *gatewayMonitor) RemoteEndpointUpdated(endpoint *v1.Endpoint) error {
	return g.RemoteEndpointCreated(endpoint)
}

func (g *gatewayMonitor) RemoteEndpointRemoved(endpoint *v1.Endpoint) error {
	lastProcessedTime, ok := g.remoteEndpointTimeStamp[endpoint.Spec.ClusterID]

	if ok && lastProcessedTime.After(endpoint.CreationTimestamp.Time) {
		logger.Infof("Ignoring deleted remote %#v since a later endpoint was already processed", endpoint)
		return nil
	}

	delete(g.remoteEndpointTimeStamp, endpoint.Spec.ClusterID)

	logger.V(log.DEBUG).Infof("Gateway monitor informed of removed endpoint: %v", endpoint)

	// Endpoint associated with remote cluster is removed, delete the associated flows.
	g.markRemoteClusterTraffic(DeleteRules, endpoint.Spec.Subnets...)

	return nil
}

func (g *gatewayMonitor) startLeaderElection() {
	if g.shuttingDown.Load() {
		return
	}

	g.controllersMutex.Lock()
	defer g.controllersMutex.Unlock()

	// Usually when leadership is lost it's due to transition to a non-gateway however if it's due to renewal failure then we try to regain
	// leadership in which case we'll still be on the gateway. Hence the isGatewayNode check here.
	if !g.State().IsOnGateway() {
		return
	}

	ctx, stop := context.WithCancel(context.Background())

	leaderElectionInfo := &LeaderElectionInfo{
		stopFunc: stop,
		stopped:  make(chan struct{}),
	}

	g.leaderElectionInfo.Store(leaderElectionInfo)

	logger.Info("On Gateway node - starting leader election")

	lock, err := resourcelock.New(resourcelock.LeasesResourceLock, g.GatewayMonitorConfig.Spec.Namespace, LeaderElectionLockName,
		g.KubeClient.CoreV1(), g.KubeClient.CoordinationV1(), resourcelock.ResourceLockConfig{
			Identity: g.nodeName + "-submariner-gateway",
		})
	utilruntime.Must(err)

	go leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   g.LeaseDuration,
		RenewDeadline:   g.RenewDeadline,
		RetryPeriod:     g.RetryPeriod,
		ReleaseOnCancel: true,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(_ context.Context) {
				err := g.startControllers() //nolint:contextcheck // Intentional to not pass context
				logger.FatalOnError(err, "Error starting the controllers")
			},
			OnStoppedLeading: func() {
				logger.Info("Leader election stopped")

				close(leaderElectionInfo.stopped)

				// We may have lost leadership due to failed renewal and not gateway node transition or shutdown, in which case we want to
				// try to regain leadership. We also stop the controllers in case there's a gateway transition during the time that
				// leadership is lost and a new instance is able to contact the API server to acquire the lock. This avoids a potential
				// window where both instances are running should this instance finally observe the gateway transition. Note that we don't
				// clear the globalnet chains to avoid datapath disruption should leadership be regained.
				if !g.shuttingDown.Load() && g.State().IsOnGateway() {
					g.stopControllers(context.Background(), false)
					g.startLeaderElection()
				}
			},
		},
	})
}

//nolint:gocyclo // Ignore cyclomatic complexity here
func (g *gatewayMonitor) startControllers() error {
	g.controllersMutex.Lock()
	defer g.controllersMutex.Unlock()

	// Since this is called asynchronously when leadership is gained, check that we're still on the gateway node and that we're
	// not shutting down. Also, we may have regained leadership so ensure the controllers weren't already started.
	if g.shuttingDown.Load() || !g.State().IsOnGateway() || len(g.controllers) > 0 {
		return nil
	}

	logger.Info("Starting controllers")

	err := g.createGlobalnetChains()
	if err != nil {
		return err
	}

	pool, err := ipam.NewIPPool(g.GatewayMonitorConfig.Spec.GlobalCIDR[0])
	if err != nil {
		return errors.Wrap(err, "error creating the IP pool")
	}

	g.controllers = nil

	c, err := NewNodeController(g.syncerConfig, pool, g.nodeName)
	if err != nil {
		return errors.Wrap(err, "error creating the Node controller")
	}

	g.controllers = append(g.controllers, c)

	c, err = NewClusterGlobalEgressIPController(g.syncerConfig, g.LocalCIDRs, pool)
	if err != nil {
		return errors.Wrap(err, "error creating the ClusterGlobalEgressIP controller")
	}

	g.controllers = append(g.controllers, c)

	c, err = NewGlobalEgressIPController(g.syncerConfig, pool)
	if err != nil {
		return errors.Wrap(err, "error creating the GlobalEgressIP controller")
	}

	g.controllers = append(g.controllers, c)

	// A user is not normally expected to delete the internal service created by the Globalnet controller.
	// However, when it's accidentally done while the globalnet controller is down, the internal service
	// remains until the finalizer is removed. We have seen that this intermediate state of
	// the service can potentially create issues in some deployments. Hence, we identify such internal
	// services and delete them when the Globalnet controller pod comes up.
	err = RemoveStaleInternalServices(g.syncerConfig)
	if err != nil {
		// Just log an error message as it's non-fatal
		logger.Errorf(err, "Error removing stale internal services created by Globalnet controller")
	}

	// The GlobalIngressIP controller needs to be started before the ServiceExport and Service controllers to ensure
	// reconciliation works properly.
	gipController, err := NewGlobalIngressIPController(g.syncerConfig, pool)
	if err != nil {
		return errors.Wrap(err, "error creating the GlobalIngressIP controller")
	}

	g.controllers = append(g.controllers, gipController)

	podControllers, err := NewIngressPodControllers(g.syncerConfig)
	if err != nil {
		return errors.Wrap(err, "error creating the IngressPodControllers")
	}

	endpointsControllers, err := NewServiceExportEndpointsControllers(g.syncerConfig)
	if err != nil {
		return errors.Wrap(err, "error creating the Endpoints controller")
	}

	ingressEndpointsControllers, err := NewIngressEndpointsControllers(g.syncerConfig)
	if err != nil {
		return errors.WithMessage(err, "error creating the IngressEndpointsControllers")
	}

	seController, err := NewServiceExportController(g.syncerConfig, podControllers, endpointsControllers,
		ingressEndpointsControllers)
	if err != nil {
		return errors.Wrap(err, "error creating the ServiceExport controller")
	}

	g.controllers = append(g.controllers, seController)

	c, err = NewServiceController(g.syncerConfig, podControllers, seController.GetSyncer(), gipController.GetSyncer())
	if err != nil {
		return errors.Wrap(err, "error creating the Service controller")
	}

	g.controllers = append(g.controllers, c)

	for _, c := range g.controllers {
		err = c.Start()
		if err != nil {
			return err //nolint:wrapcheck  // Let the caller wrap it
		}
	}

	logger.Info("Successfully started the controllers")

	return nil
}

func (g *gatewayMonitor) stopControllers(ctx context.Context, clearGlobalnetChains bool) {
	g.controllersMutex.Lock()

	logger.Infof("Stopping %d controllers", len(g.controllers))

	for _, c := range g.controllers {
		c.Stop()
	}

	g.controllers = nil

	if clearGlobalnetChains {
		g.clearGlobalnetChains()
	}

	leaderElectionInfo := g.leaderElectionInfo.Swap(&LeaderElectionInfo{})

	leaderElectionInfo.stop()

	g.controllersMutex.Unlock()

	leaderElectionInfo.awaitStopped(ctx)

	logger.Info("Controllers stopped")
}

func (g *gatewayMonitor) createNATChain(chainName string) error {
	logger.V(log.DEBUG).Infof("Install/ensure chain %q exists", chainName)

	err := g.pFilter.CreateChainIfNotExists(packetfilter.TableTypeNAT,
		&packetfilter.Chain{
			Name: chainName,
		})

	return errors.Wrapf(err, "error creating packetfilter chain %q", chainName)
}

func (g *gatewayMonitor) createGlobalnetChains() error {
	ipHookChains := []packetfilter.ChainIPHook{
		{
			Name:     constants.SmGlobalnetIngressChain,
			Type:     packetfilter.ChainTypeNAT,
			Hook:     packetfilter.ChainHookPrerouting,
			Priority: packetfilter.ChainPriorityFirst,
		},
		{
			Name:     routeAgent.SmPostRoutingChain,
			Type:     packetfilter.ChainTypeNAT,
			Hook:     packetfilter.ChainHookPostrouting,
			Priority: packetfilter.ChainPriorityFirst,
		},
	}

	for i := range ipHookChains {
		logger.V(log.DEBUG).Infof("Install/ensure IP hook chain %q exists", ipHookChains[i].Name)

		if err := g.pFilter.CreateIPHookChainIfNotExists(&ipHookChains[i]); err != nil {
			return errors.Wrapf(err, "error creating IPHook chain %q", ipHookChains[i].Name)
		}
	}

	ruleSpec := packetfilter.Rule{
		TargetChain: constants.SmGlobalnetEgressChain,
		Action:      packetfilter.RuleActionJump,
	}

	if err := g.pFilter.PrependUnique(packetfilter.TableTypeNAT, routeAgent.SmPostRoutingChain, &ruleSpec); err != nil {
		return errors.Wrapf(err, "Error prepending rule %+v", ruleSpec)
	}

	for _, chain := range []string{
		constants.SmGlobalnetEgressChain,
		constants.SmGlobalnetMarkChain,
		constants.SmGlobalnetEgressChainForPods,
		constants.SmGlobalnetEgressChainForHeadlessSvcPods,
		constants.SmGlobalnetEgressChainForHeadlessSvcEPs,
		constants.SmGlobalnetEgressChainForNamespace,
		constants.SmGlobalnetEgressChainForCluster,
	} {
		if err := g.createNATChain(chain); err != nil {
			return err
		}
	}

	if err := g.pFilter.PrependUnique(packetfilter.TableTypeNAT, constants.SmGlobalnetEgressChain,
		&packetfilter.Rule{
			TargetChain: constants.SmGlobalnetMarkChain,
			Action:      packetfilter.RuleActionJump,
		},
		&packetfilter.Rule{
			TargetChain: constants.SmGlobalnetEgressChainForPods,
			Action:      packetfilter.RuleActionJump,
		},
		&packetfilter.Rule{
			TargetChain: constants.SmGlobalnetEgressChainForHeadlessSvcPods,
			Action:      packetfilter.RuleActionJump,
		},
		&packetfilter.Rule{
			TargetChain: constants.SmGlobalnetEgressChainForHeadlessSvcEPs,
			Action:      packetfilter.RuleActionJump,
		},
		&packetfilter.Rule{
			TargetChain: constants.SmGlobalnetEgressChainForNamespace,
			Action:      packetfilter.RuleActionJump,
		},
		&packetfilter.Rule{
			TargetChain: constants.SmGlobalnetEgressChainForCluster,
			Action:      packetfilter.RuleActionJump,
		}); err != nil {
		logger.Errorf(err, "error prepending rules to the %q chain", constants.SmGlobalnetEgressChain)
	}

	return nil
}

func (g *gatewayMonitor) clearGlobalnetChains() {
	logger.Info("Active gateway migrated, flushing Globalnet chains.")

	for _, chain := range []string{
		constants.SmGlobalnetIngressChain,
		constants.SmGlobalnetEgressChain,
		constants.SmGlobalnetMarkChain,
	} {
		if err := g.pFilter.ClearChain(packetfilter.TableTypeNAT, chain); err != nil {
			logger.Errorf(err, "Error while flushing rules in chain %q", chain)
		}
	}
}

func (g *gatewayMonitor) markRemoteClusterTraffic(addRules bool, subnets ...string) {
	for _, subnet := range subnets {
		if !k8snet.IsIPv4CIDRString(subnet) {
			continue
		}

		ruleSpec := packetfilter.Rule{
			DestCIDR:  subnet,
			MarkValue: globalNetIPTableMark,
			Action:    packetfilter.RuleActionMark,
		}

		if addRules {
			logger.V(log.DEBUG).Infof("Marking traffic destined to remote cluster: %+v", &ruleSpec)

			if err := g.pFilter.AppendUnique(packetfilter.TableTypeNAT, constants.SmGlobalnetMarkChain, &ruleSpec); err != nil {
				logger.Errorf(err, "Error appending packetfilter rule %+v", &ruleSpec)
			}
		} else {
			logger.V(log.DEBUG).Infof("Deleting rule that marks remote cluster traffic: %+v", &ruleSpec)

			if err := g.pFilter.Delete(packetfilter.TableTypeNAT, constants.SmGlobalnetMarkChain, &ruleSpec); err != nil {
				logger.Errorf(err, "Error deleting iptables rule %+v", &ruleSpec)
			}
		}
	}
}

func (l *LeaderElectionInfo) stop() {
	if l.stopFunc != nil {
		l.stopFunc()
	}
}

func (l *LeaderElectionInfo) awaitStopped(ctx context.Context) {
	if l.stopped == nil {
		return
	}

	select {
	case <-l.stopped:
	case <-ctx.Done():
		logger.Warning("Timed out waiting for leader election to stop")
	}
}

func configureTCPMTUProbe() {
	// An mtuProbe value of 2 enables PLPMTUD. Along with this change, we also configure
	// base mss to 1024 as per RFC4821 recommendation.
	mtuProbe := "2"
	baseMss := "1024"

	// If we are unable to update the values, just log a warning. Most of the Globalnet
	// functionality works fine except for one use-case where Pod with HostNetworking
	// on Gateway node has mtu issues connecting to remoteServices.
	err := netlink.New().ConfigureTCPMTUProbe(mtuProbe, baseMss)
	if err != nil {
		logger.Warningf(err.Error())
	}
}
