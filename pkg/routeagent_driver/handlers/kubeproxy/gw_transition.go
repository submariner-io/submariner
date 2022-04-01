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

package kubeproxy

import (
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"k8s.io/klog"
)

// TransitionToNonGateway reconciles routes, and adds GW node IPs to
// the fdb entries on vx-submariner.
func (kp *SyncHandler) TransitionToNonGateway() error {
	klog.V(log.DEBUG).Info("The current node is no longer a Gateway")
	kp.syncHandlerMutex.Lock()
	defer kp.syncHandlerMutex.Unlock()
	kp.isGatewayNode = false
	ipAddr, err := kp.getHostIfaceIPAddress()
	if err != nil {
		klog.Errorf("unable to retrieve the IPv4 address on the Host: %v", err)
	}
	kp.gwIPs.Remove(ipAddr.String())

	// If the active Gateway transitions to a new node, we flush the HostNetwork routing table.
	kp.updateRoutingRulesForHostNetworkSupport(nil, Flush)

	err = kp.netLink.RuleDelIfPresent(netlinkAPI.NewTableRule(constants.RouteAgentHostNetworkTableID))
	if err != nil {
		klog.Errorf("Unable to delete ip rule to table %d on non-Gateway node %s: %v",
			constants.RouteAgentHostNetworkTableID, kp.hostname, err)
	}

	klog.Infof("Updating fdb entries on the vxlan interface: %s to include GW node IPs.", VxLANIface)

	err = kp.updateVxLANInterface()
	if err != nil {
		klog.Fatalf("Unable to create VxLAN interface on gateway node (%s): %v", kp.hostname, err)
	}

	if kp.vxlanDevice != nil {
		err = kp.reconcileIntraClusterRoutes()
		if err != nil {
			return errors.Wrap(err, "error while reconciling routes")
		}
	}

	return nil
}

// TransitionToGateway reconciles routes, and adds worker node IPs to
// te fdb entries on vx-submariner.
func (kp *SyncHandler) TransitionToGateway() error {
	klog.V(log.DEBUG).Info("The current node has become a Gateway")

	kp.syncHandlerMutex.Lock()
	defer kp.syncHandlerMutex.Unlock()
	kp.isGatewayNode = true
	kp.wasGatewayPreviously = true

	if kp.vxlanDevice != nil {
		err := kp.reconcileIntraClusterRoutes()
		if err != nil {
			return errors.Wrap(err, "error while reconciling routes")
		}
	}

	ipAddr, err := kp.getHostIfaceIPAddress()
	if err != nil {
		klog.Errorf("unable to retrieve the IPv4 address on the Host: %v", err)
	}
	kp.gwIPs.Add(ipAddr.String())

	klog.Infof("Updating FDB entries on the vxlan interface: %s to include worker node IPs.", VxLANIface)

	err = kp.updateVxLANInterface()
	if err != nil {
		klog.Fatalf("Unable to create VxLAN interface on gateway node (%s): %v", kp.hostname, err)
	}

	err = kp.netLink.RuleAddIfNotPresent(netlinkAPI.NewTableRule(constants.RouteAgentHostNetworkTableID))
	if err != nil {
		klog.Errorf("Unable to add ip rule to table %d on Gateway node %s: %v",
			constants.RouteAgentHostNetworkTableID, kp.hostname, err)
	}

	// Add routes to the new endpoint on the GatewayNode.
	kp.updateRoutingRulesForHostNetworkSupport(kp.remoteSubnets.Elements(), Add)

	return nil
}
