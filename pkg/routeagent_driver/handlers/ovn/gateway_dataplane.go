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
	"os"
	"strconv"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/vishvananda/netlink"
)

func (ovn *Handler) cleanupGatewayDataplane() error {
	currentRemoteSubnets, err := ovn.getExistingIPv4RuleSubnets()
	if err != nil {
		return errors.Wrapf(err, "error reading ip rule list for IPv4")
	}

	err = ovn.handleSubnets(currentRemoteSubnets.UnsortedList(), ovn.netLink.RuleDel, os.IsNotExist)
	if err != nil {
		return errors.Wrapf(err, "error removing routing rule")
	}

	defaultRoute, err := ovn.getRouteToOVNDataPlane()
	if err != nil {
		return errors.Wrap(err, "error creating default route")
	}

	err = ovn.netLink.RouteDel(defaultRoute)
	if err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "error deleting submariner default route")
	}

	return ovn.cleanupForwardingIptables()
}

func (ovn *Handler) updateGatewayDataplane() error {
	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	currentRuleRemotes, err := ovn.getExistingIPv4RuleSubnets()
	if err != nil {
		return errors.Wrapf(err, "error reading ip rule list for IPv4")
	}

	endpointSubnets := ovn.getRemoteSubnets()

	toAdd := endpointSubnets.Difference(currentRuleRemotes).UnsortedList()

	err = ovn.handleSubnets(toAdd, ovn.netLink.RuleAdd, os.IsExist)
	if err != nil {
		return errors.Wrap(err, "error adding routing rule")
	}

	toRemove := currentRuleRemotes.Difference(endpointSubnets).UnsortedList()

	err = ovn.handleSubnets(toRemove, ovn.netLink.RuleDel, os.IsNotExist)
	if err != nil {
		return errors.Wrapf(err, "error removing routing rule")
	}

	defaultRoute, err := ovn.getRouteToOVNDataPlane()
	if err != nil {
		return errors.Wrap(err, "error creating default route")
	}

	err = ovn.netLink.RouteAdd(defaultRoute)
	if err != nil && !os.IsExist(err) {
		return errors.Wrap(err, "error adding submariner default")
	}

	return ovn.setupForwardingIptables()
}

// TODO: if the #1022 workaround needs to be sustained for some time, instead of this we should be calculating
// the PMTU with a tool like tracepath between the gateway endpoints, reporting back so we can use such
// information here.
const (
	IPTCPOverHead         = 40
	ExpectedIPSECOverhead = 62
	MSSFor1500MTU         = 1500 - IPTCPOverHead - ExpectedIPSECOverhead
)

func (ovn *Handler) getForwardingRuleSpecs() ([]*packetfilter.Rule, error) {
	if ovn.cableRoutingInterface == nil {
		return nil, errors.New("error setting up forwarding packetfilter config, the cable interface isn't discovered yet, " +
			"this will be retried")
	}

	// On the Gateway node, the incoming traffic first lands on the br-ex, which includes the physical interface.
	// The OpenFlow rules on the br-ex subsequently direct Submariner traffic to the local networking stack.
	// To reroute incoming traffic over the ovn-k8s-mp0 interface, we employ routes in table 149. Before the traffic
	// hits ovn-k8s-mp0, firewall rules would be processed. Therefore, we include these firewall rules in the FORWARDing
	// chain to allow such traffic. Similar thing happens for outbound traffic as well, and we use routes in table 150.
	rules := []*packetfilter.Rule{}
	for _, remoteCIDR := range ovn.getRemoteSubnets().UnsortedList() {
		rules = append(rules, &packetfilter.Rule{
			DestCIDR:     remoteCIDR,
			Action:       packetfilter.RuleActionAccept,
			OutInterface: ovn.cableRoutingInterface.Name,
			InInterface:  OVNK8sMgmntIntfName,
		},
			&packetfilter.Rule{
				SrcCIDR:      remoteCIDR,
				Action:       packetfilter.RuleActionAccept,
				OutInterface: OVNK8sMgmntIntfName,
				InInterface:  ovn.cableRoutingInterface.Name,
			},
		)
	}

	return rules, nil
}

func (ovn *Handler) getMSSClampingRuleSpecs() ([]*packetfilter.Rule, error) {
	rules := []*packetfilter.Rule{}

	// NOTE: This is a workaround for submariner issues:
	//   * https://github.com/submariner-io/submariner/issues/1278
	//   * https://github.com/submariner-io/submariner/issues/1488
	// TODO: get the kernel to steer the ICMPs back to ovn-k8s-sub0 interface properly, or write a packet
	//       reflector in the route agent for that type of packets
	for _, remoteCIDR := range ovn.getRemoteSubnets().UnsortedList() {
		rules = append(rules, &packetfilter.Rule{
			DestCIDR:  remoteCIDR,
			Action:    packetfilter.RuleActionMss,
			ClampType: packetfilter.ToValue,
			MssValue:  strconv.Itoa(MSSFor1500MTU),
		}, &packetfilter.Rule{
			SrcCIDR:   remoteCIDR,
			Action:    packetfilter.RuleActionMss,
			ClampType: packetfilter.ToValue,
			MssValue:  strconv.Itoa(MSSFor1500MTU),
		},
		)
	}

	return rules, nil
}

type forwardRuleSpecGenerator func() ([]*packetfilter.Rule, error)

const (
	ForwardingSubmarinerMSSClampChain = "SUBMARINER-FWD-MSSCLAMP"
	ForwardingSubmarinerFWDChain      = "SUBMARINER-FORWARD"
)

func (ovn *Handler) setupForwardingIptables() error {
	if err := ovn.updateIPtableChains(packetfilter.TableTypeFilter, ForwardingSubmarinerMSSClampChain,
		ovn.getMSSClampingRuleSpecs); err != nil {
		return err
	}

	return ovn.updateIPtableChains(packetfilter.TableTypeFilter, ForwardingSubmarinerFWDChain, ovn.getForwardingRuleSpecs)
}

func (ovn *Handler) updateNoMasqueradeRules(subnet string, add bool) error {
	rules := []packetfilter.Rule{
		{
			DestCIDR: subnet,
			Action:   packetfilter.RuleActionAccept,
		},
		{
			SrcCIDR: subnet,
			Action:  packetfilter.RuleActionAccept,
		},
	}

	for i := range rules {
		var err error

		if add {
			err = ovn.pFilter.AppendUnique(packetfilter.TableTypeNAT, constants.SmPostRoutingChain, &rules[i])
		} else {
			err = ovn.pFilter.Delete(packetfilter.TableTypeNAT, constants.SmPostRoutingChain, &rules[i])
		}

		if err != nil {
			return errors.Wrapf(err, "error updating %q rule for subnet %q", constants.SmPostRoutingChain, subnet)
		}
	}

	return nil
}

func (ovn *Handler) addNoMasqueradeIPTables(subnet string) error {
	return ovn.updateNoMasqueradeRules(subnet, true)
}

func (ovn *Handler) removeNoMasqueradeIPTables(subnet string) error {
	return ovn.updateNoMasqueradeRules(subnet, false)
}

func (ovn *Handler) cleanupForwardingIptables() error {
	if err := ovn.pFilter.DeleteIPHookChain(&packetfilter.ChainIPHook{
		Name:     ForwardingSubmarinerMSSClampChain,
		Type:     packetfilter.ChainTypeFilter,
		Hook:     packetfilter.ChainHookForward,
		Priority: packetfilter.ChainPriorityFirst,
	}); err != nil {
		return errors.Wrapf(err, "error clearing chain %q", ForwardingSubmarinerMSSClampChain)
	}

	return errors.Wrapf(ovn.pFilter.DeleteIPHookChain(&packetfilter.ChainIPHook{
		Name:     ForwardingSubmarinerFWDChain,
		Type:     packetfilter.ChainTypeFilter,
		Hook:     packetfilter.ChainHookForward,
		Priority: packetfilter.ChainPriorityFirst,
	}), "error clearing chain %q", ForwardingSubmarinerFWDChain)
}

func (ovn *Handler) getRouteToOVNDataPlane() (*netlink.Route, error) {
	nextHop, err := ovn.getNextHopOnK8sMgmtIntf()
	if err != nil {
		return nil, errors.Wrapf(err, "getNextHopOnK8sMgmtIntf returned error")
	}

	return &netlink.Route{
		Gw:    *nextHop,
		Table: constants.RouteAgentInterClusterNetworkTableID,
	}, nil
}

func (ovn *Handler) initIPtablesChains() error {
	logger.V(log.DEBUG).Infof("Install/ensure %q/%s IPHook chain exists", constants.SmPostRoutingChain, "NAT")

	if err := ovn.pFilter.CreateIPHookChainIfNotExists(&packetfilter.ChainIPHook{
		Name:     constants.SmPostRoutingChain,
		Type:     packetfilter.ChainTypeNAT,
		Hook:     packetfilter.ChainHookPostrouting,
		Priority: packetfilter.ChainPriorityFirst,
	}); err != nil {
		return errors.Wrapf(err, "error installing %q IPHook chain", constants.SmPostRoutingChain)
	}

	if err := ovn.ensureForwardChains(); err != nil {
		return errors.Wrap(err, "error ensuring FORWARD sub-chain entries")
	}

	return nil
}

func (ovn *Handler) ensureForwardChains() error {
	for _, chain := range []string{ForwardingSubmarinerFWDChain, ForwardingSubmarinerMSSClampChain} {
		if err := ovn.pFilter.CreateIPHookChainIfNotExists(&packetfilter.ChainIPHook{
			Name:     chain,
			Type:     packetfilter.ChainTypeFilter,
			Hook:     packetfilter.ChainHookForward,
			Priority: packetfilter.ChainPriorityFirst,
		}); err != nil {
			return errors.Wrapf(err, "error installing forwarding IPHook chain %q", chain)
		}
	}

	return nil
}

func (ovn *Handler) updateIPtableChains(table packetfilter.TableType, chain string, ruleGen forwardRuleSpecGenerator) error {
	ruleSpecs, err := ruleGen()
	if err != nil {
		return err
	}

	return errors.Wrap(ovn.pFilter.UpdateChainRules(table, chain, ruleSpecs), "error updating chain rules")
}
