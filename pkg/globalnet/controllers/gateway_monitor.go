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
	"fmt"
	"os"
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/stringset"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	admUtil "github.com/submariner-io/admiral/pkg/util"
	"github.com/submariner-io/admiral/pkg/watcher"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cidr"
	"github.com/submariner-io/submariner/pkg/ipam"
	"github.com/submariner-io/submariner/pkg/iptables"
	"github.com/submariner-io/submariner/pkg/netlink"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog"
)

func NewGatewayMonitor(spec Specification, localCIDRs []string, config watcher.Config) (Interface, error) {
	gatewayMonitor := &gatewayMonitor{
		baseController: newBaseController(),
		spec:           spec,
		isGatewayNode:  false,
		localSubnets:   stringset.New(localCIDRs...).Elements(),
		remoteSubnets:  stringset.NewSynchronized(),
	}

	var err error

	gatewayMonitor.ipt, err = iptables.New()
	if err != nil {
		return nil, err
	}

	if config.RestMapper == nil {
		if config.RestMapper, err = admUtil.BuildRestMapper(config.RestConfig); err != nil {
			return nil, err
		}
	}

	if config.Client == nil {
		if config.Client, err = dynamic.NewForConfig(config.RestConfig); err != nil {
			return nil, fmt.Errorf("error creating dynamic client: %v", err)
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
			SourceNamespace: spec.Namespace,
		},
	}

	gatewayMonitor.endpointWatcher, err = watcher.New(&config)
	if err != nil {
		return nil, err
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
	klog.Info("Starting GatewayMonitor to monitor the active Gateway node in the cluster.")

	err := g.endpointWatcher.Start(g.stopCh)
	if err != nil {
		return err
	}

	if err := g.createGlobalNetMarkingChain(); err != nil {
		return fmt.Errorf("error while calling createGlobalNetMarkingChain: %v", err)
	}

	return nil
}

func (g *gatewayMonitor) Stop() {
	klog.Info("GatewayMonitor stopping")

	g.baseController.Stop()

	g.syncMutex.Lock()
	g.stopControllers()
	g.syncMutex.Unlock()
}

func (g *gatewayMonitor) handleCreatedOrUpdatedEndpoint(obj runtime.Object, numRequeues int) bool {
	endpoint := obj.(*v1.Endpoint)

	klog.V(log.DEBUG).Infof("In processNextEndpoint, endpoint info: %+v", endpoint)

	if endpoint.Spec.ClusterID != g.spec.ClusterID {
		klog.V(log.DEBUG).Infof("Endpoint %q, host: %q belongs to a remote cluster",
			endpoint.Spec.ClusterID, endpoint.Spec.Hostname)

		overlap, err := cidr.IsOverlapping(endpoint.Spec.Subnets, g.spec.GlobalCIDR[0])
		if err != nil {
			// Ideally this case will never hit, as the subnets are valid CIDRs
			klog.Warningf("unable to validate overlapping Service CIDR: %s", err)
		}

		if overlap {
			// When GlobalNet is used, globalCIDRs allocated to the clusters should not overlap.
			// If they overlap, skip the endpoint as its an invalid configuration which is not supported.
			klog.Errorf("GlobalCIDR %q of local cluster %q overlaps with remote cluster %s",
				g.spec.GlobalCIDR[0], g.spec.ClusterID, endpoint.Spec.ClusterID)

			return false
		}

		for _, remoteSubnet := range endpoint.Spec.Subnets {
			g.remoteSubnets.Add(remoteSubnet)
			g.markRemoteClusterTraffic(remoteSubnet, AddRules)
		}

		return false
	}

	hostname, err := os.Hostname()
	if err != nil {
		klog.Fatalf("Unable to determine hostname: %v", err)
	}

	for _, remoteSubnet := range g.remoteSubnets.Elements() {
		g.markRemoteClusterTraffic(remoteSubnet, AddRules)
	}

	// If the endpoint hostname matches with our hostname, it implies we are on gateway node
	if endpoint.Spec.Hostname == hostname {
		klog.V(log.DEBUG).Infof("Transitioned to gateway node with endpoint private IP %s", endpoint.Spec.PrivateIP)

		configureTCPMTUProbe()

		g.syncMutex.Lock()
		if !g.isGatewayNode {
			g.isGatewayNode = true

			err := g.startControllers()
			if err != nil {
				klog.Fatalf("Error starting the controllers: %v", err)
			}
		}
		g.syncMutex.Unlock()
	} else {
		klog.V(log.DEBUG).Infof("Transitioned to non-gateway node with endpoint private IP %s", endpoint.Spec.PrivateIP)

		g.syncMutex.Lock()
		if g.isGatewayNode {
			g.stopControllers()
			g.isGatewayNode = false
		}
		g.syncMutex.Unlock()
	}

	return false
}

func (g *gatewayMonitor) handleRemovedEndpoint(obj runtime.Object, numRequeues int) bool {
	endpoint := obj.(*v1.Endpoint)

	klog.V(log.DEBUG).Infof("Informed of removed endpoint for gateway monitor: %v", endpoint)

	hostname, err := os.Hostname()
	if err != nil {
		klog.Fatalf("Could not retrieve hostname: %v", err)
	}

	if endpoint.Spec.Hostname == hostname && endpoint.Spec.ClusterID == g.spec.ClusterID {
		g.syncMutex.Lock()
		if g.isGatewayNode {
			g.stopControllers()
			g.isGatewayNode = false
		}
		g.syncMutex.Unlock()
	} else if endpoint.Spec.ClusterID != g.spec.ClusterID {
		// Endpoint associated with remote cluster is removed, delete the associated flows.
		for _, remoteSubnet := range endpoint.Spec.Subnets {
			g.remoteSubnets.Remove(remoteSubnet)
			g.markRemoteClusterTraffic(remoteSubnet, DeleteRules)
		}
	}

	return false
}

func (g *gatewayMonitor) startControllers() error {
	klog.Infof("On Gateway node - starting controllers")

	err := g.createGlobalnetChains()
	if err != nil {
		return err
	}

	pool, err := ipam.NewIPPool(g.spec.GlobalCIDR[0])
	if err != nil {
		return err
	}

	g.controllers = nil

	c, err := NewNodeController(*g.syncerConfig, pool, g.nodeName)
	if err != nil {
		return errors.WithMessage(err, "error creating the Node controller")
	}

	g.controllers = append(g.controllers, c)

	c, err = NewClusterGlobalEgressIPController(*g.syncerConfig, g.localSubnets, pool)
	if err != nil {
		return errors.WithMessage(err, "error creating the ClusterGlobalEgressIP controller")
	}

	g.controllers = append(g.controllers, c)

	c, err = NewGlobalEgressIPController(*g.syncerConfig, pool)
	if err != nil {
		return errors.WithMessage(err, "error creating the GlobalEgressIP controller")
	}

	g.controllers = append(g.controllers, c)

	c, err = NewServiceExportController(*g.syncerConfig)
	if err != nil {
		return errors.WithMessage(err, "error creating the ServiceExport controller")
	}

	g.controllers = append(g.controllers, c)

	c, err = NewServiceController(*g.syncerConfig)
	if err != nil {
		return errors.WithMessage(err, "error creating the Service controller")
	}

	g.controllers = append(g.controllers, c)

	c, err = NewGlobalIngressIPController(*g.syncerConfig, pool)
	if err != nil {
		return errors.WithMessage(err, "error creating the GlobalIngressIP controller")
	}

	g.controllers = append(g.controllers, c)

	for _, c := range g.controllers {
		err = c.Start()
		if err != nil {
			return err
		}
	}

	klog.Infof("Successfully started the controllers")

	return nil
}

func (g *gatewayMonitor) stopControllers() {
	for _, c := range g.controllers {
		c.Stop()
	}

	g.controllers = nil

	g.clearGlobalnetChains()
}

func (g *gatewayMonitor) createGlobalNetMarkingChain() error {
	klog.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetMarkChain)

	if err := util.CreateChainIfNotExists(g.ipt, "nat", constants.SmGlobalnetMarkChain); err != nil {
		return fmt.Errorf("error creating iptables chain %s: %v", constants.SmGlobalnetMarkChain, err)
	}

	return nil
}

func (g *gatewayMonitor) createGlobalnetChains() error {
	klog.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetIngressChain)

	if err := util.CreateChainIfNotExists(g.ipt, "nat", constants.SmGlobalnetIngressChain); err != nil {
		return fmt.Errorf("error creating iptables chain %s: %v", constants.SmGlobalnetIngressChain, err)
	}

	forwardToSubGlobalNetChain := []string{"-j", constants.SmGlobalnetIngressChain}
	if err := util.PrependUnique(g.ipt, "nat", "PREROUTING", forwardToSubGlobalNetChain); err != nil {
		klog.Errorf("error appending iptables rule %q: %v\n", strings.Join(forwardToSubGlobalNetChain, " "), err)
	}

	klog.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetEgressChain)

	if err := util.CreateChainIfNotExists(g.ipt, "nat", constants.SmGlobalnetEgressChain); err != nil {
		return fmt.Errorf("error creating iptables chain %s: %v", constants.SmGlobalnetEgressChain, err)
	}

	klog.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmPostRoutingChain)

	if err := util.CreateChainIfNotExists(g.ipt, "nat", constants.SmPostRoutingChain); err != nil {
		return fmt.Errorf("error creating iptables chain %s: %v", constants.SmPostRoutingChain, err)
	}

	forwardToSubGlobalNetChain = []string{"-j", constants.SmGlobalnetEgressChain}
	if err := util.PrependUnique(g.ipt, "nat", constants.SmPostRoutingChain, forwardToSubGlobalNetChain); err != nil {
		klog.Errorf("error inserting iptables rule %q: %v\n", strings.Join(forwardToSubGlobalNetChain, " "), err)
	}

	if err := g.createGlobalNetMarkingChain(); err != nil {
		return err
	}

	forwardToSubGlobalNetChain = []string{"-j", constants.SmGlobalnetMarkChain}
	if err := util.PrependUnique(g.ipt, "nat", constants.SmGlobalnetEgressChain, forwardToSubGlobalNetChain); err != nil {
		klog.Errorf("error inserting iptables rule %q: %v\n", strings.Join(forwardToSubGlobalNetChain, " "), err)
	}

	klog.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetEgressChainForPods)

	if err := util.CreateChainIfNotExists(g.ipt, "nat", constants.SmGlobalnetEgressChainForPods); err != nil {
		return fmt.Errorf("error creating iptables chain %s: %v", constants.SmGlobalnetEgressChainForPods, err)
	}

	klog.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetEgressChainForHeadlessSvcPods)

	if err := util.CreateChainIfNotExists(g.ipt, "nat", constants.SmGlobalnetEgressChainForHeadlessSvcPods); err != nil {
		return fmt.Errorf("error creating iptables chain %s: %v", constants.SmGlobalnetEgressChainForHeadlessSvcPods, err)
	}

	klog.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetEgressChainForNamespace)

	if err := util.CreateChainIfNotExists(g.ipt, "nat", constants.SmGlobalnetEgressChainForNamespace); err != nil {
		return fmt.Errorf("error creating iptables chain %s: %v", constants.SmGlobalnetEgressChainForNamespace, err)
	}

	klog.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetEgressChainForCluster)

	if err := util.CreateChainIfNotExists(g.ipt, "nat", constants.SmGlobalnetEgressChainForCluster); err != nil {
		return fmt.Errorf("error creating iptables chain %s: %v", constants.SmGlobalnetEgressChainForCluster, err)
	}

	forwardToSubGlobalNetChain = []string{"-j", constants.SmGlobalnetEgressChainForPods}
	if err := util.InsertUnique(g.ipt, "nat", constants.SmGlobalnetEgressChain, 2, forwardToSubGlobalNetChain); err != nil {
		klog.Errorf("error inserting iptables rule %q: %v\n", strings.Join(forwardToSubGlobalNetChain, " "), err)
	}

	forwardToSubGlobalNetChain = []string{"-j", constants.SmGlobalnetEgressChainForHeadlessSvcPods}
	if err := util.InsertUnique(g.ipt, "nat", constants.SmGlobalnetEgressChain, 3, forwardToSubGlobalNetChain); err != nil {
		klog.Errorf("error inserting iptables rule %q: %v\n", strings.Join(forwardToSubGlobalNetChain, " "), err)
	}

	forwardToSubGlobalNetChain = []string{"-j", constants.SmGlobalnetEgressChainForNamespace}
	if err := util.InsertUnique(g.ipt, "nat", constants.SmGlobalnetEgressChain, 4, forwardToSubGlobalNetChain); err != nil {
		klog.Errorf("error inserting iptables rule %q: %v\n", strings.Join(forwardToSubGlobalNetChain, " "), err)
	}

	forwardToSubGlobalNetChain = []string{"-j", constants.SmGlobalnetEgressChainForCluster}
	if err := util.InsertUnique(g.ipt, "nat", constants.SmGlobalnetEgressChain, 5, forwardToSubGlobalNetChain); err != nil {
		klog.Errorf("error inserting iptables rule %q: %v\n", strings.Join(forwardToSubGlobalNetChain, " "), err)
	}

	return nil
}

func (g *gatewayMonitor) clearGlobalnetChains() {
	klog.Info("Active gateway migrated, flushing Globalnet chains.")

	if err := g.ipt.ClearChain("nat", constants.SmGlobalnetIngressChain); err != nil {
		klog.Errorf("Error while flushing rules in %s chain: %v", constants.SmGlobalnetIngressChain, err)
	}

	if err := g.ipt.ClearChain("nat", constants.SmGlobalnetEgressChain); err != nil {
		klog.Errorf("Error while flushing rules in %s chain: %v", constants.SmGlobalnetEgressChain, err)
	}

	if err := g.ipt.ClearChain("nat", constants.SmGlobalnetMarkChain); err != nil {
		klog.Errorf("Error while flushing rules in %s chain: %v", constants.SmGlobalnetMarkChain, err)
	}
}

func (g *gatewayMonitor) markRemoteClusterTraffic(remoteCidr string, addRules bool) {
	ruleSpec := []string{"-d", remoteCidr, "-j", "MARK", "--set-mark", globalNetIPTableMark}

	if addRules {
		klog.V(log.DEBUG).Infof("Marking traffic destined to remote cluster: %s", strings.Join(ruleSpec, " "))

		if err := g.ipt.AppendUnique("nat", constants.SmGlobalnetMarkChain, ruleSpec...); err != nil {
			klog.Errorf("error appending iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
		}
	} else {
		klog.V(log.DEBUG).Infof("Deleting rule that marks remote cluster traffic: %s", strings.Join(ruleSpec, " "))
		if err := g.ipt.Delete("nat", constants.SmGlobalnetMarkChain, ruleSpec...); err != nil {
			klog.Errorf("error deleting iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
		}
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
		klog.Warningf(err.Error())
	}
}
