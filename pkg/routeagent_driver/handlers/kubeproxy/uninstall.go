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
	"github.com/submariner-io/submariner/pkg/iptables"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	"github.com/submariner-io/submariner/pkg/port"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/vishvananda/netlink"
	"k8s.io/klog"
)

func (kp *SyncHandler) Stop(uninstall bool) error {
	if !uninstall {
		return nil
	}

	klog.Infof("Uninstalling Submariner changes from the node %q", kp.hostname)
	klog.Infof("Flushing route table %d entries", constants.RouteAgentHostNetworkTableID)

	err := kp.netLink.FlushRouteTable(constants.RouteAgentHostNetworkTableID)
	if err != nil {
		// We can safely ignore this error, as this table will exist only on GW nodes
		klog.V(log.TRACE).Infof("Flushing routing table %d returned error. Can be ignored on non-Gw node: %v",
			constants.RouteAgentHostNetworkTableID, err)
	}

	err = kp.netLink.RuleDelIfPresent(netlinkAPI.NewTableRule(constants.RouteAgentHostNetworkTableID))
	if err != nil {
		klog.V(log.TRACE).Infof("Deleting IP Rule pointing to %d table returned error: %v",
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

	klog.Infof("Deleting the %q interface", VxLANIface)

	err := netlinkAPI.New().LinkDel(iface)
	if err != nil {
		klog.Errorf("Failed to delete the the vxlan interface %q: %v", VxLANIface, err)
	}
}

func deleteIPTableChains() {
	ipt, err := iptables.New()
	if err != nil {
		klog.Errorf("Failed to initialize IPTable interface: %v", err)
		return
	}

	klog.Infof("Flushing iptable entries in %q chain of %q table", constants.SmPostRoutingChain, constants.NATTable)

	if err := ipt.ClearChain(constants.NATTable, constants.SmPostRoutingChain); err != nil {
		klog.Errorf("Error flushing iptables chain %q of %q table: %v", constants.SmPostRoutingChain,
			constants.NATTable, err)
	}

	klog.Infof("Deleting iptable entry in %q chain of %q table", constants.PostRoutingChain, constants.NATTable)

	ruleSpec := []string{"-j", constants.SmPostRoutingChain}
	if err := ipt.Delete(constants.NATTable, constants.PostRoutingChain, ruleSpec...); err != nil {
		klog.Errorf("Error deleting iptables rule from %q chain: %v", constants.PostRoutingChain, err)
	}

	klog.Infof("Deleting iptable %q chain of %q table", constants.SmPostRoutingChain, constants.NATTable)

	if err := ipt.DeleteChain(constants.NATTable, constants.SmPostRoutingChain); err != nil {
		klog.Errorf("Error deleting iptable chain %q of table %q: %v", constants.SmPostRoutingChain,
			constants.NATTable, err)
	}

	klog.Infof("Flushing iptable entries in %q chain of %q table", constants.SmInputChain, constants.FilterTable)

	if err := ipt.ClearChain(constants.FilterTable, constants.SmInputChain); err != nil {
		klog.Errorf("Error flushing iptables chain %q of %q table: %v", constants.SmInputChain,
			constants.FilterTable, err)
	}

	klog.Infof("Deleting iptable entry in %q chain of %q table", constants.InputChain, constants.FilterTable)

	ruleSpec = []string{"-p", "udp", "-m", "udp", "-j", constants.SmInputChain}
	if err := ipt.Delete(constants.FilterTable, constants.InputChain, ruleSpec...); err != nil {
		klog.Errorf("Error deleting iptables rule from %q chain: %v", constants.InputChain, err)
	}

	klog.Infof("Deleting iptable %q chain of %q table", constants.SmInputChain, constants.FilterTable)

	if err := ipt.DeleteChain(constants.FilterTable, constants.SmInputChain); err != nil {
		klog.Errorf("Error deleting iptable chain %q of table %q: %v", constants.SmInputChain,
			constants.FilterTable, err)
	}
}
