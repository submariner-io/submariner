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
	"net"

	"github.com/submariner-io/admiral/pkg/log"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	"github.com/submariner-io/submariner/pkg/port"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/vishvananda/netlink"
)

func (kp *SyncHandler) Uninstall() error {
	logger.Infof("Uninstalling Submariner changes from the node %q", kp.hostname)
	logger.Infof("Flushing route table %d entries", constants.RouteAgentHostNetworkTableID)

	err := kp.netLink.FlushRouteTable(constants.RouteAgentHostNetworkTableID)
	if err != nil {
		// We can safely ignore this error, as this table will exist only on GW nodes
		logger.V(log.TRACE).Infof("Flushing routing table %d returned error. Can be ignored on non-Gw node: %v",
			constants.RouteAgentHostNetworkTableID, err)
	}

	err = kp.netLink.RuleDelIfPresent(netlinkAPI.NewTableRule(constants.RouteAgentHostNetworkTableID))
	if err != nil {
		logger.V(log.TRACE).Infof("Deleting IP Rule pointing to %d table returned error: %v",
			constants.RouteAgentHostNetworkTableID, err)
	}

	deleteVxLANInterface()
	deleteIPTableChains()

	return nil
}

func deleteVxLANInterface() {
	iface := &netlink.Vxlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:  VxLANIface,
			Flags: net.FlagUp,
		},
		VxlanId: 100,
		SrcAddr: nil,
		Port:    port.IntraClusterVxLAN,
	}

	logger.Infof("Deleting the %q interface", VxLANIface)

	err := netlinkAPI.New().LinkDel(iface)
	if err != nil {
		logger.Errorf(err, "Failed to delete the vxlan interface %q", VxLANIface)
	}
}

func deleteIPTableChains() {
	pFilter, err := packetfilter.New()
	if err != nil {
		logger.Errorf(err, "Failed to initialize packetfilter interface")
		return
	}

	logger.Infof("Flushing packetfilter entries in %q chain of %q table", constants.SmPostRoutingChain, constants.NATTable)

	if err := pFilter.ClearChain(packetfilter.TableTypeNAT, constants.SmPostRoutingChain); err != nil {
		logger.Errorf(err, "Error flushing packetfilter chain %q of %q table", constants.SmPostRoutingChain,
			constants.NATTable)
	}

	logger.Infof("Deleting iptable entry in %q chain of %q table", constants.PostRoutingChain, constants.NATTable)

	chain := packetfilter.ChainIPHook{
		Name:     constants.SmPostRoutingChain,
		Type:     packetfilter.ChainTypeNAT,
		Hook:     packetfilter.ChainHookPostrouting,
		Priority: packetfilter.ChainPriorityFirst,
	}
	if err := pFilter.DeleteIPHookChain(&chain); err != nil {
		logger.Errorf(err, "Error deleting IPHook chain %q of %q table", constants.SmPostRoutingChain,
			constants.NATTable)
	}

	logger.Infof("Flushing iptable entries in %q chain of %q table", constants.SmInputChain, constants.FilterTable)

	if err := pFilter.ClearChain(packetfilter.TableTypeFilter, constants.SmInputChain); err != nil {
		logger.Errorf(err, "Error flushing packetfilter chain %q of %q table", constants.SmInputChain,
			constants.FilterTable)
	}

	logger.Infof("Deleting iptable entry in %q chain of %q table", constants.InputChain, constants.FilterTable)

	chain = packetfilter.ChainIPHook{
		Name:     constants.SmInputChain,
		Type:     packetfilter.ChainTypeFilter,
		Hook:     packetfilter.ChainHookInput,
		Priority: packetfilter.ChainPriorityLast,
		JumpRule: &packetfilter.Rule{
			Proto:       packetfilter.RuleProtoUDP,
			Action:      packetfilter.RuleActionJump,
			TargetChain: constants.SmInputChain,
		},
	}
	if err := pFilter.DeleteIPHookChain(&chain); err != nil {
		logger.Errorf(err, "Error deleting IPHook chain %q of %q table", constants.InputChain,
			constants.FilterTable)
	}
}
