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
package ipam

import (
	"fmt"
	"os"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/stringset"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	admUtil "github.com/submariner-io/admiral/pkg/util"
	"github.com/submariner-io/admiral/pkg/watcher"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cidr"
	"github.com/submariner-io/submariner/pkg/globalnet/cleanup"
	"github.com/submariner-io/submariner/pkg/iptables"
	"github.com/submariner-io/submariner/pkg/netlink"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog"
)

func NewGatewayMonitor(spec *SubmarinerIPAMControllerSpecification, config watcher.Config) (*GatewayMonitor, error) {
	gatewayMonitor := &GatewayMonitor{
		clusterID:     spec.ClusterID,
		ipamSpec:      spec,
		isGatewayNode: false,
		remoteSubnets: stringset.NewSynchronized(),
	}

	iptableHandler, err := iptables.New()
	if err != nil {
		return nil, err
	}

	gatewayMonitor.ipt = iptableHandler

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

func (gm *GatewayMonitor) Start(stopCh <-chan struct{}) error {
	klog.Info("Starting GatewayMonitor to monitor the active Gateway node in the cluster.")

	err := gm.endpointWatcher.Start(stopCh)
	if err != nil {
		return err
	}

	if err := CreateGlobalNetMarkingChain(gm.ipt); err != nil {
		return fmt.Errorf("error while calling createGlobalNetMarkingChain: %v", err)
	}

	return nil
}

func (gm *GatewayMonitor) Stop() {
	klog.Info("GatewayMonitor stopping")

	gm.syncMutex.Lock()
	gm.stopIPAMController()
	gm.syncMutex.Unlock()
}

func (gm *GatewayMonitor) handleCreatedOrUpdatedEndpoint(obj runtime.Object, numRequeues int) bool {
	endpoint := obj.(*v1.Endpoint)

	klog.V(log.DEBUG).Infof("In processNextEndpoint, endpoint info: %+v", endpoint)

	if endpoint.Spec.ClusterID != gm.clusterID {
		klog.V(log.DEBUG).Infof("Endpoint %s belongs to a remote cluster", endpoint.Spec.Hostname)

		overlap, err := cidr.IsOverlapping(endpoint.Spec.Subnets, gm.ipamSpec.GlobalCIDR[0])
		if err != nil {
			// Ideally this case will never hit, as the subnets are valid CIDRs
			klog.Warningf("unable to validate overlapping Service CIDR: %s", err)
		}

		if overlap {
			// When GlobalNet is used, globalCIDRs allocated to the clusters should not overlap.
			// If they overlap, skip the endpoint as its an invalid configuration which is not supported.
			klog.Errorf("GlobalCIDR %q of local cluster %q overlaps with remote cluster %s",
				gm.ipamSpec.GlobalCIDR[0], gm.ipamSpec.ClusterID, endpoint.Spec.ClusterID)

			return false
		}

		for _, remoteSubnet := range endpoint.Spec.Subnets {
			gm.remoteSubnets.Add(remoteSubnet)
			MarkRemoteClusterTraffic(gm.ipt, remoteSubnet, AddRules)
		}

		return false
	}

	hostname, err := os.Hostname()
	if err != nil {
		klog.Fatalf("Unable to determine hostname: %v", err)
	}

	for _, remoteSubnet := range gm.remoteSubnets.Elements() {
		MarkRemoteClusterTraffic(gm.ipt, remoteSubnet, AddRules)
	}

	// If the endpoint hostname matches with our hostname, it implies we are on gateway node
	if endpoint.Spec.Hostname == hostname {
		klog.V(log.DEBUG).Infof("We are now on GatewayNode %s", endpoint.Spec.PrivateIP)

		configureTCPMTUProbe()

		gm.syncMutex.Lock()
		if !gm.isGatewayNode {
			gm.isGatewayNode = true
			gm.initializeIPAMController(gm.ipamSpec.GlobalCIDR[0], gm.nodeName)
		}
		gm.syncMutex.Unlock()
	} else {
		klog.V(log.DEBUG).Infof("We are on non-gatewayNode. GatewayNode ip is %s", endpoint.Spec.PrivateIP)

		gm.syncMutex.Lock()
		if gm.isGatewayNode {
			gm.stopIPAMController()
			gm.isGatewayNode = false
		}
		gm.syncMutex.Unlock()
	}

	return false
}

func (gm *GatewayMonitor) handleRemovedEndpoint(obj runtime.Object, numRequeues int) bool {
	endpoint := obj.(*v1.Endpoint)

	klog.V(log.DEBUG).Infof("Informed of removed endpoint for gateway monitor: %v", endpoint)

	hostname, err := os.Hostname()
	if err != nil {
		klog.Fatalf("Could not retrieve hostname: %v", err)
	}

	if endpoint.Spec.Hostname == hostname && endpoint.Spec.ClusterID == gm.clusterID {
		gm.syncMutex.Lock()
		if gm.isGatewayNode {
			gm.stopIPAMController()
			gm.isGatewayNode = false
		}
		gm.syncMutex.Unlock()
	} else if endpoint.Spec.ClusterID != gm.clusterID {
		// Endpoint associated with remote cluster is removed, delete the associated flows.
		for _, remoteSubnet := range endpoint.Spec.Subnets {
			gm.remoteSubnets.Remove(remoteSubnet)
			MarkRemoteClusterTraffic(gm.ipt, remoteSubnet, DeleteRules)
		}
	}

	return false
}

func (gm *GatewayMonitor) initializeIPAMController(globalCIDR, gwNodeName string) {
	klog.V(log.DEBUG).Infof("On Gateway Node, initializing ipamController.")

	ipamController, err := NewController(gm.ipamSpec, *gm.syncerConfig, globalCIDR, gwNodeName)
	if err != nil {
		klog.Fatalf("Error creating controller: %s", err.Error())
	}

	gm.stopProcessing = make(chan struct{})

	if err = ipamController.Start(gm.stopProcessing); err != nil {
		klog.Fatalf("Error running ipamController: %s", err.Error())
	}

	klog.V(log.DEBUG).Infof("Successfully started the ipamController")
}

func (gm *GatewayMonitor) stopIPAMController() {
	if gm.stopProcessing != nil {
		klog.V(log.DEBUG).Infof("Stopping ipamController")
		close(gm.stopProcessing)
		gm.stopProcessing = nil

		cleanup.ClearGlobalnetChains(gm.ipt)
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
