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

	"github.com/pkg/errors"
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

	return ovn.setupForwardingIptables()
}

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

type forwardRuleSpecGenerator func() ([][]string, error)

const (
	forwardingSubmarinerFWDChain = "SUBMARINER-FORWARD"
)

func (ovn *Handler) setupForwardingIptables() error {
	return ovn.updateIPtableChains("filter", forwardingSubmarinerFWDChain, ovn.getForwardingRuleSpecs)
}

func (ovn *Handler) addNoMasqueradeIPTables(subnet string) error {
	return errors.Wrapf(ovn.ipt.AppendUnique("nat", constants.SmPostRoutingChain,
		[]string{"-d", subnet, "-j", "ACCEPT"}...), "error updating %q rules for subnet %q",
		constants.SmPostRoutingChain, subnet)
}

func (ovn *Handler) removeNoMasqueradeIPTables(subnet string) error {
	return errors.Wrapf(ovn.ipt.Delete("nat", constants.SmPostRoutingChain,
		[]string{"-d", subnet, "-j", "ACCEPT"}...), "error updating %q rules for subnet %q",
		constants.SmPostRoutingChain, subnet)
}

func (ovn *Handler) cleanupForwardingIptables() error {
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
	if err := ovn.ipt.CreateChainIfNotExists("filter", forwardingSubmarinerFWDChain); err != nil {
		return errors.Wrapf(err, "error creating chain %q", forwardingSubmarinerFWDChain)
	}

	return errors.Wrapf(ovn.ipt.InsertUnique("filter", "FORWARD", 1, []string{"-j", forwardingSubmarinerFWDChain}),
		"error inserting rule for chain %q", forwardingSubmarinerFWDChain)
}

func (ovn *Handler) updateIPtableChains(table, chain string, ruleGen forwardRuleSpecGenerator) error {
	ruleSpecs, err := ruleGen()
	if err != nil {
		return err
	}

	return errors.Wrap(ovn.ipt.UpdateChainRules(table, chain, ruleSpecs), "error updating chain rules")
}
