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
	"github.com/submariner-io/submariner/pkg/packetfilter"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/ovn/vsctl"
	"github.com/vishvananda/netlink"
)

func (ovn *Handler) Stop() error {
	ovn.gatewayRouteController.stop()
	ovn.nonGatewayRouteController.stop()
	close(ovn.stopCh)

	return nil
}

func (ovn *Handler) Uninstall() error {
	logger.Infof("Uninstalling OVN components from the node")

	err := ovn.cleanupRoutes()
	if err != nil {
		logger.Errorf(err, "Error cleaning the routes")
	}

	err = ovn.netLink.FlushRouteTable(constants.RouteAgentInterClusterNetworkTableID)
	if err != nil {
		logger.Errorf(err, "Flushing routing table %d returned error",
			constants.RouteAgentInterClusterNetworkTableID)
	}

	err = ovn.netLink.FlushRouteTable(constants.RouteAgentHostNetworkTableID)
	if err != nil {
		logger.Errorf(err, "Flushing routing table %d returned error",
			constants.RouteAgentHostNetworkTableID)
	}

	if err := ovn.pFilter.ClearChain(packetfilter.TableTypeFilter, ForwardingSubmarinerFWDChain); err != nil {
		logger.Errorf(err, "Error flushing packetfilter chain %q of %q table", ForwardingSubmarinerFWDChain, "Filter")
	}

	if err := ovn.pFilter.DeleteIPHookChain(&packetfilter.ChainIPHook{
		Name:     ForwardingSubmarinerFWDChain,
		Type:     packetfilter.ChainTypeFilter,
		Hook:     packetfilter.ChainHookForward,
		Priority: packetfilter.ChainPriorityFirst,
	}); err != nil {
		logger.Errorf(err, "DeleteIPHookChain %s returned error",
			ForwardingSubmarinerFWDChain)
	}

	if err := ovn.pFilter.ClearChain(packetfilter.TableTypeNAT, constants.SmPostRoutingChain); err != nil {
		logger.Errorf(err, "Error flushing packetfilter chain %q of %q table", constants.SmPostRoutingChain, "NAT")
	}

	if err := ovn.pFilter.DeleteIPHookChain(&packetfilter.ChainIPHook{
		Name:     constants.SmPostRoutingChain,
		Type:     packetfilter.ChainTypeNAT,
		Hook:     packetfilter.ChainHookPostrouting,
		Priority: packetfilter.ChainPriorityFirst,
	}); err != nil {
		logger.Errorf(err, "DeleteIPHookChain %s returned error",
			constants.SmPostRoutingChain)
	}

	return nil
}

func (ovn *Handler) cleanupRoutes() error {
	rules, err := ovn.netLink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return errors.Wrapf(err, "error listing rules")
	}

	for i := range rules {
		if rules[i].Table == constants.RouteAgentInterClusterNetworkTableID || rules[i].Table == constants.RouteAgentHostNetworkTableID {
			err = ovn.netLink.RuleDel(&rules[i])
			if err != nil {
				return errors.Wrapf(err, "error deleting the rule %v", rules[i])
			}
		}
	}

	return nil
}

func (ovn *Handler) LegacyCleanup() {
	err := vsctl.DelInternalPort(ovnK8sSubmarinerBridge, ovnK8sSubmarinerInterface)
	if err != nil {
		logger.Errorf(err, "Error deleting Submariner port %q", ovnK8sSubmarinerInterface)
	}

	err = vsctl.DelBridge(ovnK8sSubmarinerBridge)
	if err != nil {
		logger.Errorf(err, "Error deleting Submariner bridge %q", ovnK8sSubmarinerBridge)
	}
}
