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
	"github.com/submariner-io/submariner/pkg/routeagent_driver/chains"
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

	err = kp.netLink.RuleDelIfPresent(netlinkAPI.NewTableRule(constants.RouteAgentHostNetworkTableID, kp.ipFamily))
	if err != nil {
		logger.V(log.TRACE).Infof("Deleting IP Rule pointing to %d table returned error: %v",
			constants.RouteAgentHostNetworkTableID, err)
	}

	kp.deleteVxLANInterface()
	kp.deleteIPTableChains()

	return nil
}

func (kp *SyncHandler) deleteVxLANInterface() {
	iface := &netlink.Vxlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:  kp.vxlanIface,
			Flags: net.FlagUp,
		},
		VxlanId: 100,
		SrcAddr: nil,
		Port:    port.IntraClusterVxLAN,
	}

	logger.Infof("Deleting the %q interface", kp.vxlanIface)

	err := netlinkAPI.New().LinkDel(iface)
	if err != nil {
		logger.Errorf(err, "Failed to delete the vxlan interface %q", kp.vxlanIface)
	}
}

func (kp *SyncHandler) deleteIPTableChains() {
	pFilter, err := packetfilter.New(kp.ipFamily)
	if err != nil {
		logger.Errorf(err, "Failed to initialize packetfilter interface")
		return
	}

	logger.Infof("Flushing IPv%v packetfilter entries in %q chain of %q table", kp.ipFamily, chains.SmPostRouting, constants.NATTable)

	if err := pFilter.ClearChain(packetfilter.TableTypeNAT, chains.SmPostRouting); err != nil {
		logger.Errorf(err, "Error flushing packetfilter chain %q of %q table", chains.SmPostRouting,
			constants.NATTable)
	}

	logger.Infof("Deleting IPv%v packetfilter entry in %q chain of %q table", kp.ipFamily, chains.SmPostRouting, constants.NATTable)

	chain := packetfilter.ChainIPHook{
		Name:     chains.SmPostRouting,
		Type:     packetfilter.ChainTypeNAT,
		Hook:     packetfilter.ChainHookPostrouting,
		Priority: packetfilter.ChainPriorityFirst,
	}
	if err := pFilter.DeleteIPHookChain(&chain); err != nil {
		logger.Errorf(err, "Error deleting IPv%v IPHook chain %q of %q table", kp.ipFamily, chains.SmPostRouting,
			constants.NATTable)
	}

	logger.Infof("Flushing IPv%v packetfilter entries in %q chain of %q table", kp.ipFamily, chains.SmInput, constants.FilterTable)

	if err := pFilter.ClearChain(packetfilter.TableTypeFilter, chains.SmInput); err != nil {
		logger.Errorf(err, "Error flushing IPv%v packetfilter chain %q of %q table", kp.ipFamily, chains.SmInput,
			constants.FilterTable)
	}

	logger.Infof("Deleting IPv%v packetfilter entry in %q chain of %q table", kp.ipFamily, chains.SmInput, constants.FilterTable)

	chain = packetfilter.ChainIPHook{
		Name:     chains.SmInput,
		Type:     packetfilter.ChainTypeFilter,
		Hook:     packetfilter.ChainHookInput,
		Priority: packetfilter.ChainPriorityLast,
		JumpRule: &packetfilter.Rule{
			Proto:       packetfilter.RuleProtoUDP,
			Action:      packetfilter.RuleActionJump,
			TargetChain: chains.SmInput,
		},
	}
	if err := pFilter.DeleteIPHookChain(&chain); err != nil {
		logger.Errorf(err, "Error deleting IPv%v IPHook chain %q of %q table", kp.ipFamily, chains.SmInput,
			constants.FilterTable)
	}

	logger.Infof("Flushing IPv%v packetfilter entries in %q chain of %q table", kp.ipFamily, chains.SmSelfSnat, constants.NATTable)

	if err := pFilter.ClearChain(packetfilter.TableTypeNAT, chains.SmSelfSnat); err != nil {
		logger.Errorf(err, "Error flushing IPv%v packetfilter chain %q of %q table", kp.ipFamily, chains.SmSelfSnat,
			constants.NATTable)
	}

	chain = packetfilter.ChainIPHook{
		Name:     chains.SmSelfSnat,
		Type:     packetfilter.ChainTypeNAT,
		Hook:     packetfilter.ChainHookPostrouting,
		Priority: packetfilter.ChainPriorityMiddle,
	}

	if err := pFilter.DeleteIPHookChain(&chain); err != nil {
		logger.Errorf(err, "Error deleting IPv%v IPHook chain %q of %q table", kp.ipFamily, chains.SmSelfSnat,
			constants.NATTable)
	}

	logger.Infof("Flushing IPv%v packetfilter entries in %q chain of %q table", kp.ipFamily, chains.SmForward, constants.FilterTable)

	if err := pFilter.ClearChain(packetfilter.TableTypeFilter, chains.SmForward); err != nil {
		logger.Errorf(err, "Error flushing IPv%v packetfilter chain %q of %q table", kp.ipFamily, chains.SmForward,
			constants.FilterTable)
	}

	chain = packetfilter.ChainIPHook{
		Name:     chains.SmForward,
		Type:     packetfilter.ChainTypeFilter,
		Hook:     packetfilter.ChainHookForward,
		Priority: packetfilter.ChainPriorityFirst,
	}

	if err := pFilter.DeleteIPHookChain(&chain); err != nil {
		logger.Errorf(err, "Error deleting IPv%v IPHook chain %q of %q table", kp.ipFamily, chains.SmForward,
			constants.FilterTable)
	}
}
