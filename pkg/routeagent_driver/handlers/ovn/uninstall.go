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

package ovn

import (
	"github.com/pkg/errors"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/ovn/vsctl"
	"github.com/vishvananda/netlink"
	"k8s.io/klog/v2"
)

func (ovn *Handler) Stop(uninstall bool) error {
	if !uninstall {
		return nil
	}

	klog.Infof("Uninstalling OVN components from the node")

	err := vsctl.DelInternalPort(ovnK8sSubmarinerBridge, ovnK8sSubmarinerInterface)
	if err != nil {
		klog.Errorf("Error deleting Submariner port %q due to %v", ovnK8sSubmarinerInterface, err)
	}

	err = vsctl.DelBridge(ovnK8sSubmarinerBridge)
	if err != nil {
		klog.Errorf("Error deleting Submariner bridge %q due to %v", ovnK8sSubmarinerBridge, err)
	}

	err = ovn.cleanupRoutes()
	if err != nil {
		klog.Errorf("Error cleaning the routes %v", err)
	}

	err = ovn.netlink.FlushRouteTable(constants.RouteAgentInterClusterNetworkTableID)
	if err != nil {
		klog.Errorf("Flushing routing table %d returned error: %v",
			constants.RouteAgentInterClusterNetworkTableID, err)
	}

	err = ovn.netlink.FlushRouteTable(constants.RouteAgentHostNetworkTableID)
	if err != nil {
		klog.Errorf("Flushing routing table %d returned error: %v",
			constants.RouteAgentHostNetworkTableID, err)
	}

	err = ovn.cleanupForwardingIptables()
	if err != nil {
		klog.Errorf("Error deleting iptable rules %v", err)
	}

	return nil
}

func (ovn *Handler) cleanupRoutes() error {
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return errors.Wrapf(err, "error listing rules")
	}

	for i := range rules {
		if rules[i].Table == constants.RouteAgentInterClusterNetworkTableID || rules[i].Table == constants.RouteAgentHostNetworkTableID {
			err = netlink.RuleDel(&rules[i])
			if err != nil {
				return errors.Wrapf(err, "error deleting the rule %v", rules[i])
			}
		}
	}

	return nil
}
