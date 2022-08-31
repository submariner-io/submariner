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
	"net"
	"os"
	"strconv"

	"github.com/pkg/errors"
	submiptables "github.com/submariner-io/submariner/pkg/iptables"
	npSyncerOvn "github.com/submariner-io/submariner/pkg/networkplugin-syncer/handlers/ovn"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	iptcommon "github.com/submariner-io/submariner/pkg/routeagent_driver/iptables"
	"github.com/vishvananda/netlink"
)

func (ovn *Handler) cleanupGatewayDataplane() error {
	currentRemoteSubnets, err := ovn.getExistingIPv4RuleSubnets()
	if err != nil {
		return errors.Wrapf(err, "error reading ip rule list for IPv4")
	}

	err = ovn.handleSubnets(currentRemoteSubnets.Elements(), netlink.RuleDel, os.IsNotExist)
	if err != nil {
		return errors.Wrapf(err, "error removing routing rule")
	}

	err = netlink.RouteDel(ovn.getSubmDefaultRoute())
	if err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "error deleting submariner default route")
	}

	return ovn.cleanupForwardingIptables()
}

func (ovn *Handler) updateGatewayDataplane() error {
	currentRuleRemotes, err := ovn.getExistingIPv4RuleSubnets()
	if err != nil {
		return errors.Wrapf(err, "error reading ip rule list for IPv4")
	}

	endpointSubnets := ovn.getRemoteSubnets()

	toAdd := currentRuleRemotes.Difference(endpointSubnets)

	err = ovn.handleSubnets(toAdd, netlink.RuleAdd, os.IsExist)
	if err != nil {
		return errors.Wrap(err, "error adding routing rule")
	}

	toRemove := endpointSubnets.Difference(currentRuleRemotes)

	err = ovn.handleSubnets(toRemove, netlink.RuleDel, os.IsNotExist)
	if err != nil {
		return errors.Wrapf(err, "error removing routing rule")
	}

	err = netlink.RouteAdd(ovn.getSubmDefaultRoute())
	if err != nil && !os.IsExist(err) {
		return errors.Wrap(err, "error adding submariner default")
	}

	if err = ovn.updateNoMasqueradeIPTables(); err != nil {
		return errors.Wrap(err, "error handling no-masquerade rules")
	}

	return ovn.setupForwardingIptables()
}

// TODO: if the #1022 workaround needs to be sustained for some time, instead of this we should be calculating
//       the PMTU with a tool like tracepath between the gateway endpoints, reporting back so we can use such
//		 information here.
const (
	IPTCPOverHead         = 40
	ExpectedIPSECOverhead = 62
	MSSFor1500MTU         = 1500 - IPTCPOverHead - ExpectedIPSECOverhead
)

func (ovn *Handler) getForwardingRuleSpecs() ([][]string, error) {
	if ovn.cableRoutingInterface == nil {
		return nil, errors.New("error setting up forwarding iptables, the cable interface isn't discovered yet, " +
			"this will be retried")
	}

	rules := [][]string{
		{"-i", ovnK8sSubmarinerInterface, "-o", ovn.cableRoutingInterface.Name, "-j", "ACCEPT"},
		{"-i", ovn.cableRoutingInterface.Name, "-o", ovnK8sSubmarinerInterface, "-j", "ACCEPT"},
	}

	return rules, nil
}

func (ovn *Handler) getMSSClampingRuleSpecs() ([][]string, error) {
	rules := [][]string{}

	// NOTE: This is a workaround for submariner issues:
	//   * https://github.com/submariner-io/submariner/issues/1278
	//   * https://github.com/submariner-io/submariner/issues/1488
	// TODO: get the kernel to steer the ICMPs back to ovn-k8s-sub0 interface properly, or write a packet
	//       reflector in the route agent for that type of packets
	for _, remoteCIDR := range ovn.getRemoteSubnets().Elements() {
		rules = append(rules,
			[]string{
				"-d", remoteCIDR, "-p", "tcp", "-m", "tcp",
				"--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS", "--set-mss", strconv.Itoa(MSSFor1500MTU),
			},
			[]string{
				"-s", remoteCIDR, "-p", "tcp", "-m", "tcp",
				"--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS", "--set-mss", strconv.Itoa(MSSFor1500MTU),
			})
	}

	// NOTE: This is a workaround for submariner issue https://github.com/submariner-io/submariner/issues/1022
	// TODO: work with the core-ovn community to make sure that load balancers propagate ICMPs back to pods
	for _, serviceCIDR := range ovn.config.ServiceCidr {
		rules = append(rules, []string{
			"-o", ovnK8sSubmarinerInterface, "-d", serviceCIDR, "-p", "tcp", "-m", "tcp",
			"--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS", "--set-mss", strconv.Itoa(MSSFor1500MTU),
		})
	}

	return rules, nil
}

type forwardRuleSpecGenerator func() ([][]string, error)

const (
	forwardingSubmarinerMSSClampChain = "SUBMARINER-FWD-MSSCLAMP"
	forwardingSubmarinerFWDChain      = "SUBMARINER-FORWARD"
)

func (ovn *Handler) setupForwardingIptables() error {
	if err := ovn.updateIPtableChains("filter", forwardingSubmarinerMSSClampChain, ovn.getMSSClampingRuleSpecs); err != nil {
		return err
	}

	return ovn.updateIPtableChains("filter", forwardingSubmarinerFWDChain, ovn.getForwardingRuleSpecs)
}

func (ovn *Handler) updateNoMasqueradeIPTables() error {
	rules := ovn.getNoMasqueradRuleSpecs()

	return errors.Wrapf(submiptables.UpdateChainRules(ovn.ipt, "nat", constants.SmPostRoutingChain, rules),
		"error updating %q rules", constants.SmPostRoutingChain)
}

func (ovn *Handler) getNoMasqueradRuleSpecs() [][]string {
	var rules [][]string

	for _, endpoint := range ovn.remoteEndpoints {
		for _, subnet := range endpoint.Spec.Subnets {
			rules = append(rules, []string{"-d", subnet, "-j", "ACCEPT"})
		}
	}

	return rules
}

func (ovn *Handler) cleanupForwardingIptables() error {
	if err := ovn.ipt.ClearChain("filter", forwardingSubmarinerMSSClampChain); err != nil {
		return errors.Wrapf(err, "error clearing chain %q", forwardingSubmarinerMSSClampChain)
	}

	return errors.Wrapf(ovn.ipt.ClearChain("filter", forwardingSubmarinerFWDChain),
		"error clearing chain %q", forwardingSubmarinerFWDChain)
}

func (ovn *Handler) getSubmDefaultRoute() *netlink.Route {
	return &netlink.Route{
		Gw:    net.ParseIP(npSyncerOvn.SubmarinerUpstreamIP),
		Table: constants.RouteAgentInterClusterNetworkTableID,
	}
}

func (ovn *Handler) initIPtablesChains() error {
	if err := iptcommon.InitSubmarinerPostRoutingChain(ovn.ipt); err != nil {
		return errors.Wrap(err, "error initializing POST routing chain")
	}

	if err := ovn.ensureForwardChains(); err != nil {
		return errors.Wrap(err, "error ensuring FORWARD sub-chain entries")
	}

	return nil
}

func (ovn *Handler) ensureForwardChains() error {
	if err := ovn.ipt.CreateChainIfNotExists("filter", forwardingSubmarinerMSSClampChain); err != nil {
		return errors.Wrapf(err, "error creating chain %q", forwardingSubmarinerMSSClampChain)
	}

	if err := submiptables.InsertUnique(ovn.ipt, "filter", "FORWARD", 1,
		[]string{"-j", forwardingSubmarinerMSSClampChain}); err != nil {
		return errors.Wrapf(err, "error inserting rule for chain %q", forwardingSubmarinerMSSClampChain)
	}

	if err := ovn.ipt.CreateChainIfNotExists("filter", forwardingSubmarinerFWDChain); err != nil {
		return errors.Wrapf(err, "error creating chain %q", forwardingSubmarinerFWDChain)
	}

	return errors.Wrapf(submiptables.InsertUnique(ovn.ipt, "filter", "FORWARD", 2, []string{"-j", forwardingSubmarinerFWDChain}),
		"error inserting rule for chain %q", forwardingSubmarinerFWDChain)
}

func (ovn *Handler) updateIPtableChains(table, chain string, ruleGen forwardRuleSpecGenerator) error {
	ruleSpecs, err := ruleGen()
	if err != nil {
		return err
	}

	return errors.Wrap(submiptables.UpdateChainRules(ovn.ipt, table, chain, ruleSpecs), "error updating chain rules")
}
