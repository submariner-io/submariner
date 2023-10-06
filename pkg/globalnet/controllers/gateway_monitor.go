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
	"strings"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	admUtil "github.com/submariner-io/admiral/pkg/util"
	"github.com/submariner-io/admiral/pkg/watcher"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cidr"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/ipam"
	"github.com/submariner-io/submariner/pkg/iptables"
	"github.com/submariner-io/submariner/pkg/netlink"
	routeAgent "github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	k8snet "k8s.io/utils/net"
	"k8s.io/utils/set"
)

func NewGatewayMonitor(config *GatewayMonitorConfig) (Interface, error) {
	// We'll panic if config is nil, this is intentional
	gatewayMonitor := &gatewayMonitor{
		baseController:          newBaseController(),
		spec:                    config.Spec,
		kubeClient:              config.KubeClient,
		isGatewayNode:           atomic.Bool{},
		shuttingDown:            atomic.Bool{},
		localSubnets:            set.New(config.LocalCIDRs...).UnsortedList(),
		remoteSubnets:           set.New[string](),
		remoteEndpointTimeStamp: map[string]metav1.Time{},
		leaderElectionInfo:      atomic.Pointer[LeaderElectionInfo]{},
		LeaderElectionConfig:    config.LeaderElectionConfig,
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

	gatewayMonitor.ipt, err = iptables.New()
	if err != nil {
		return nil, errors.Wrap(err, "error creating IP tables")
	}

	if config.RestMapper == nil {
		if config.RestMapper, err = admUtil.BuildRestMapper(config.RestConfig); err != nil {
			return nil, errors.Wrap(err, "error creating the RestMapper")
		}
	}

	if config.Client == nil {
		if config.Client, err = dynamic.NewForConfig(config.RestConfig); err != nil {
			return nil, errors.Wrap(err, "error creating dynamic client")
		}
	}

	if config.Scheme == nil {
		config.Scheme = scheme.Scheme
	}

	config.ResourceConfigs = []watcher.ResourceConfig{
		{
			Name:         "IPAM GatewayMonitor",
			ResourceType: &v1.Endpoint{},
			Handler: watcher.EventHandlerFuncs{
				OnCreateFunc: gatewayMonitor.handleCreatedOrUpdatedEndpoint,
				OnUpdateFunc: gatewayMonitor.handleCreatedOrUpdatedEndpoint,
				OnDeleteFunc: gatewayMonitor.handleRemovedEndpoint,
			},
			SourceNamespace: config.Spec.Namespace,
		},
	}

	gatewayMonitor.endpointWatcher, err = watcher.New(&config.Config)
	if err != nil {
		return nil, errors.Wrap(err, "error creating the Endpoint watcher")
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

	return gatewayMonitor, nil
}

func (g *gatewayMonitor) Start() error {
	logger.Info("Starting GatewayMonitor to monitor the active Gateway node in the cluster.")

	if err := g.createGlobalNetMarkingChain(); err != nil {
		return errors.Wrap(err, "error while calling createGlobalNetMarkingChain")
	}

	err := g.endpointWatcher.Start(g.stopCh)
	if err != nil {
		return errors.Wrap(err, "error starting the Endpoint watcher")
	}

	return nil
}

func (g *gatewayMonitor) Stop() {
	logger.Info("GatewayMonitor stopping")

	g.shuttingDown.Store(true)

	g.baseController.Stop()

	// stopControllers should be pretty quick but put a deadline on it, so we don't block shutdown for a long time.

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	g.stopControllers(ctx)
}

func (g *gatewayMonitor) handleCreatedOrUpdatedEndpoint(obj runtime.Object, _ int) bool {
	endpoint := obj.(*v1.Endpoint)

	logger.V(log.DEBUG).Infof("Gateway monitor informed of create/updated endpoint: %v", endpoint)

	if endpoint.Spec.ClusterID != g.spec.ClusterID {
		lastProcessedTime, ok := g.remoteEndpointTimeStamp[endpoint.Spec.ClusterID]

		if ok && lastProcessedTime.After(endpoint.CreationTimestamp.Time) {
			logger.Infof("Ignoring new remote %#v since a later endpoint was already"+
				"processed", endpoint)
			return false
		}

		logger.V(log.DEBUG).Infof("Endpoint %q, host: %q belongs to a remote cluster",
			endpoint.Spec.ClusterID, endpoint.Spec.Hostname)

		overlap, err := cidr.IsOverlapping(endpoint.Spec.Subnets, g.spec.GlobalCIDR[0])
		if err != nil {
			// Ideally this case will never hit, as the subnets are valid CIDRs
			logger.Warningf("unable to validate overlapping Service CIDR: %s", err)
		}

		if overlap {
			// When GlobalNet is used, globalCIDRs allocated to the clusters should not overlap.
			// If they overlap, skip the endpoint as its an invalid configuration which is not supported.
			logger.Errorf(nil, "GlobalCIDR %q of local cluster %q overlaps with remote cluster %s",
				g.spec.GlobalCIDR[0], g.spec.ClusterID, endpoint.Spec.ClusterID)

			return false
		}

		for _, remoteSubnet := range endpoint.Spec.Subnets {
			if k8snet.IsIPv4CIDRString(remoteSubnet) {
				g.remoteSubnets.Insert(remoteSubnet)
				g.markRemoteClusterTraffic(remoteSubnet, AddRules)
			}
		}

		g.remoteEndpointTimeStamp[endpoint.Spec.ClusterID] = endpoint.CreationTimestamp

		return false
	}

	hostname, err := os.Hostname()
	if err != nil {
		logger.Fatalf("Unable to determine hostname: %v", err)
	}

	for _, remoteSubnet := range g.remoteSubnets.UnsortedList() {
		g.markRemoteClusterTraffic(remoteSubnet, AddRules)
	}

	// If the endpoint hostname matches with our hostname, it implies we are on gateway node
	if endpoint.Spec.Hostname == hostname {
		configureTCPMTUProbe()

		if g.isGatewayNode.CompareAndSwap(false, true) {
			logger.Infof("Transitioned to gateway node %q with endpoint private IP %s", hostname, endpoint.Spec.PrivateIP)

			g.startLeaderElection()
		}
	} else if g.isGatewayNode.CompareAndSwap(true, false) {
		logger.Infof("Transitioned to non-gateway node %q", endpoint.Spec.Hostname)

		g.stopControllers(context.Background())
	}

	return false
}

func (g *gatewayMonitor) handleRemovedEndpoint(obj runtime.Object, _ int) bool {
	endpoint := obj.(*v1.Endpoint)

	lastProcessedTime, ok := g.remoteEndpointTimeStamp[endpoint.Spec.ClusterID]

	if ok && lastProcessedTime.After(endpoint.CreationTimestamp.Time) {
		logger.Infof("Ignoring deleted remote %#v since a later endpoint was already"+
			"processed", endpoint)
		return false
	}

	delete(g.remoteEndpointTimeStamp, endpoint.Spec.ClusterID)

	logger.V(log.DEBUG).Infof("Gateway monitor informed of removed endpoint: %v", endpoint)

	hostname, err := os.Hostname()
	if err != nil {
		logger.Fatalf("Could not retrieve hostname: %v", err)
	}

	if endpoint.Spec.Hostname == hostname && endpoint.Spec.ClusterID == g.spec.ClusterID {
		if g.isGatewayNode.CompareAndSwap(true, false) {
			logger.Infof("Gateway node %q endpoint removed", hostname)

			g.stopControllers(context.Background())
		}
	} else if endpoint.Spec.ClusterID != g.spec.ClusterID {
		// Endpoint associated with remote cluster is removed, delete the associated flows.
		for _, remoteSubnet := range endpoint.Spec.Subnets {
			if k8snet.IsIPv4CIDRString(remoteSubnet) {
				g.remoteSubnets.Delete(remoteSubnet)
				g.markRemoteClusterTraffic(remoteSubnet, DeleteRules)
			}
		}
	}

	return false
}

func (g *gatewayMonitor) startLeaderElection() {
	if g.shuttingDown.Load() {
		return
	}

	g.controllersMutex.Lock()
	defer g.controllersMutex.Unlock()

	// Usually when leadership is lost it's due to transition to a non-gateway however if it's due to renewal failure then we try to regain
	// leadership in which case we'll still be on the gateway. Hence the isGatewayNode check here.
	if !g.isGatewayNode.Load() {
		return
	}

	ctx, stop := context.WithCancel(context.Background())

	leaderElectionInfo := &LeaderElectionInfo{
		stopFunc: stop,
		stopped:  make(chan struct{}),
	}

	g.leaderElectionInfo.Store(leaderElectionInfo)

	logger.Info("On Gateway node - starting leader election")

	lock, err := resourcelock.New(resourcelock.LeasesResourceLock, g.spec.Namespace, LeaderElectionLockName,
		g.kubeClient.CoreV1(), g.kubeClient.CoordinationV1(), resourcelock.ResourceLockConfig{
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
				if err != nil {
					logger.Fatalf("Error starting the controllers: %v", err)
				}
			},
			OnStoppedLeading: func() {
				logger.Info("Leader election stopped")

				close(leaderElectionInfo.stopped)

				// We may have lost leadership due to failed renewal and not gateway node transition or shutdown, in which case we want to
				// try to regain leadership.
				g.startLeaderElection()
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
	if g.shuttingDown.Load() || !g.isGatewayNode.Load() || len(g.controllers) > 0 {
		return nil
	}

	logger.Info("Starting controllers")

	err := g.createGlobalnetChains()
	if err != nil {
		return err
	}

	pool, err := ipam.NewIPPool(g.spec.GlobalCIDR[0])
	if err != nil {
		return errors.Wrap(err, "error creating the IP pool")
	}

	g.controllers = nil

	c, err := NewNodeController(g.syncerConfig, pool, g.nodeName)
	if err != nil {
		return errors.Wrap(err, "error creating the Node controller")
	}

	g.controllers = append(g.controllers, c)

	c, err = NewClusterGlobalEgressIPController(g.syncerConfig, g.localSubnets, pool)
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
	c, err = NewGlobalIngressIPController(g.syncerConfig, pool)
	if err != nil {
		return errors.Wrap(err, "error creating the GlobalIngressIP controller")
	}

	g.controllers = append(g.controllers, c)

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

	c, err = NewServiceController(g.syncerConfig, podControllers, seController.GetSyncer())
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

func (g *gatewayMonitor) stopControllers(ctx context.Context) {
	g.controllersMutex.Lock()

	logger.Infof("Stopping %d controllers", len(g.controllers))

	for _, c := range g.controllers {
		c.Stop()
	}

	g.controllers = nil

	g.clearGlobalnetChains()

	leaderElectionInfo := g.leaderElectionInfo.Swap(&LeaderElectionInfo{})

	leaderElectionInfo.stop()

	g.controllersMutex.Unlock()

	leaderElectionInfo.awaitStopped(ctx)

	logger.Info("Controllers stopped")
}

func (g *gatewayMonitor) createGlobalNetMarkingChain() error {
	logger.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetMarkChain)

	if err := g.ipt.CreateChainIfNotExists("nat", constants.SmGlobalnetMarkChain); err != nil {
		return errors.Wrapf(err, "error creating iptables chain %s", constants.SmGlobalnetMarkChain)
	}

	return nil
}

//nolint:gocyclo // Lots of error checks, but simple logic
func (g *gatewayMonitor) createGlobalnetChains() error {
	logger.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetIngressChain)

	if err := g.ipt.CreateChainIfNotExists("nat", constants.SmGlobalnetIngressChain); err != nil {
		return errors.Wrapf(err, "error creating iptables chain %s", constants.SmGlobalnetIngressChain)
	}

	forwardToSubGlobalNetChain := []string{"-j", constants.SmGlobalnetIngressChain}
	if err := g.ipt.PrependUnique("nat", "PREROUTING", forwardToSubGlobalNetChain); err != nil {
		logger.Errorf(err, "Error appending iptables rule %q", strings.Join(forwardToSubGlobalNetChain, " "))
	}

	logger.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetEgressChain)

	if err := g.ipt.CreateChainIfNotExists("nat", constants.SmGlobalnetEgressChain); err != nil {
		return errors.Wrapf(err, "error creating iptables chain %s", constants.SmGlobalnetEgressChain)
	}

	logger.V(log.DEBUG).Infof("Install/ensure %s chain exists", routeAgent.SmPostRoutingChain)

	if err := g.ipt.CreateChainIfNotExists("nat", routeAgent.SmPostRoutingChain); err != nil {
		return errors.Wrapf(err, "error creating iptables chain %s", routeAgent.SmPostRoutingChain)
	}

	forwardToSubGlobalNetChain = []string{"-j", constants.SmGlobalnetEgressChain}
	if err := g.ipt.PrependUnique("nat", routeAgent.SmPostRoutingChain, forwardToSubGlobalNetChain); err != nil {
		logger.Errorf(err, "Error inserting iptables rule %q", strings.Join(forwardToSubGlobalNetChain, " "))
	}

	if err := g.createGlobalNetMarkingChain(); err != nil {
		return err
	}

	forwardToSubGlobalNetChain = []string{"-j", constants.SmGlobalnetMarkChain}
	if err := g.ipt.PrependUnique("nat", constants.SmGlobalnetEgressChain, forwardToSubGlobalNetChain); err != nil {
		logger.Errorf(err, "Error inserting iptables rule %q", strings.Join(forwardToSubGlobalNetChain, " "))
	}

	logger.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetEgressChainForPods)

	if err := g.ipt.CreateChainIfNotExists("nat", constants.SmGlobalnetEgressChainForPods); err != nil {
		return errors.Wrapf(err, "error creating iptables chain %s", constants.SmGlobalnetEgressChainForPods)
	}

	logger.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetEgressChainForHeadlessSvcPods)

	if err := g.ipt.CreateChainIfNotExists("nat", constants.SmGlobalnetEgressChainForHeadlessSvcPods); err != nil {
		return errors.Wrapf(err, "error creating iptables chain %s", constants.SmGlobalnetEgressChainForHeadlessSvcPods)
	}

	logger.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetEgressChainForHeadlessSvcEPs)

	if err := g.ipt.CreateChainIfNotExists("nat", constants.SmGlobalnetEgressChainForHeadlessSvcEPs); err != nil {
		return errors.Wrapf(err, "error creating iptables chain %s", constants.SmGlobalnetEgressChainForHeadlessSvcEPs)
	}

	logger.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetEgressChainForNamespace)

	if err := g.ipt.CreateChainIfNotExists("nat", constants.SmGlobalnetEgressChainForNamespace); err != nil {
		return errors.Wrapf(err, "error creating iptables chain %s", constants.SmGlobalnetEgressChainForNamespace)
	}

	logger.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetEgressChainForCluster)

	if err := g.ipt.CreateChainIfNotExists("nat", constants.SmGlobalnetEgressChainForCluster); err != nil {
		return errors.Wrapf(err, "error creating iptables chain %s", constants.SmGlobalnetEgressChainForCluster)
	}

	forwardToSubGlobalNetChain = []string{"-j", constants.SmGlobalnetEgressChainForPods}
	if err := g.ipt.InsertUnique("nat", constants.SmGlobalnetEgressChain, 2, forwardToSubGlobalNetChain); err != nil {
		logger.Errorf(err, "Error inserting iptables rule %q", strings.Join(forwardToSubGlobalNetChain, " "))
	}

	forwardToSubGlobalNetChain = []string{"-j", constants.SmGlobalnetEgressChainForHeadlessSvcPods}
	if err := g.ipt.InsertUnique("nat", constants.SmGlobalnetEgressChain, 3, forwardToSubGlobalNetChain); err != nil {
		logger.Errorf(err, "Error inserting iptables rule %q", strings.Join(forwardToSubGlobalNetChain, " "))
	}

	forwardToSubGlobalNetChain = []string{"-j", constants.SmGlobalnetEgressChainForHeadlessSvcEPs}
	if err := g.ipt.InsertUnique("nat", constants.SmGlobalnetEgressChain, 4, forwardToSubGlobalNetChain); err != nil {
		logger.Errorf(err, "Error inserting iptables rule %q", strings.Join(forwardToSubGlobalNetChain, " "))
	}

	forwardToSubGlobalNetChain = []string{"-j", constants.SmGlobalnetEgressChainForNamespace}
	if err := g.ipt.InsertUnique("nat", constants.SmGlobalnetEgressChain, 5, forwardToSubGlobalNetChain); err != nil {
		logger.Errorf(err, "Error inserting iptables rule %q", strings.Join(forwardToSubGlobalNetChain, " "))
	}

	forwardToSubGlobalNetChain = []string{"-j", constants.SmGlobalnetEgressChainForCluster}
	if err := g.ipt.InsertUnique("nat", constants.SmGlobalnetEgressChain, 6, forwardToSubGlobalNetChain); err != nil {
		logger.Errorf(err, "Error inserting iptables rule %q", strings.Join(forwardToSubGlobalNetChain, " "))
	}

	return nil
}

func (g *gatewayMonitor) clearGlobalnetChains() {
	logger.Info("Active gateway migrated, flushing Globalnet chains.")

	if err := g.ipt.ClearChain("nat", constants.SmGlobalnetIngressChain); err != nil {
		logger.Errorf(err, "Error while flushing rules in %s chain", constants.SmGlobalnetIngressChain)
	}

	if err := g.ipt.ClearChain("nat", constants.SmGlobalnetEgressChain); err != nil {
		logger.Errorf(err, "Error while flushing rules in %s chain", constants.SmGlobalnetEgressChain)
	}

	if err := g.ipt.ClearChain("nat", constants.SmGlobalnetMarkChain); err != nil {
		logger.Errorf(err, "Error while flushing rules in %s chain", constants.SmGlobalnetMarkChain)
	}
}

func (g *gatewayMonitor) markRemoteClusterTraffic(remoteCidr string, addRules bool) {
	ruleSpec := []string{"-d", remoteCidr, "-j", "MARK", "--set-mark", globalNetIPTableMark}

	if addRules {
		logger.V(log.DEBUG).Infof("Marking traffic destined to remote cluster: %s", strings.Join(ruleSpec, " "))

		if err := g.ipt.AppendUnique("nat", constants.SmGlobalnetMarkChain, ruleSpec...); err != nil {
			logger.Errorf(err, "Error appending iptables rule \"%s\"", strings.Join(ruleSpec, " "))
		}
	} else {
		logger.V(log.DEBUG).Infof("Deleting rule that marks remote cluster traffic: %s", strings.Join(ruleSpec, " "))
		if err := g.ipt.Delete("nat", constants.SmGlobalnetMarkChain, ruleSpec...); err != nil {
			logger.Errorf(err, "Error deleting iptables rule \"%s\"", strings.Join(ruleSpec, " "))
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
