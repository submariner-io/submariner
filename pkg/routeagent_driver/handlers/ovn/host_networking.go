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
	"fmt"
	"net"
	"os"
	"syscall"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/vishvananda/netlink"
	"k8s.io/utils/set"
)

const (
	OVNK8sMgmntIntfName = "ovn-k8s-mp0"
)

func (ovn *Handler) updateHostNetworkDataplane() error {
	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	currentRuleRemotes, err := ovn.getExistingIPv4HostNetworkRoutes()
	if err != nil {
		return errors.Wrapf(err, "error reading ip rule list for IPv4")
	}

	endpointSubnets := ovn.getRemoteSubnets()

	toAdd := endpointSubnets.Difference(currentRuleRemotes).UnsortedList()

	err = ovn.programRulesForRemoteSubnets(toAdd, ovn.netLink.RuleAdd, os.IsExist)
	if err != nil {
		return errors.Wrap(err, "error adding routing rule")
	}

	toRemove := currentRuleRemotes.Difference(endpointSubnets).UnsortedList()

	err = ovn.programRulesForRemoteSubnets(toRemove, ovn.netLink.RuleDel, os.IsNotExist)
	if err != nil {
		return errors.Wrapf(err, "error removing routing rule")
	}

	nextHop, err := ovn.getNextHopOnK8sMgmtIntf()
	if err != nil {
		return errors.Wrapf(err, "getNextHopOnK8sMgmtIntf returned error")
	}

	route := &netlink.Route{
		Gw:    *nextHop,
		Table: constants.RouteAgentHostNetworkTableID,
	}

	err = ovn.netLink.RouteAdd(route)
	if err != nil && !os.IsExist(err) {
		return errors.Wrap(err, "error adding submariner default")
	}

	return nil
}

func (ovn *Handler) getExistingIPv4HostNetworkRoutes() (set.Set[string], error) {
	currentRuleRemotes := set.New[string]()

	rules, err := ovn.netLink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return nil, errors.Wrapf(err, "error listing rules")
	}

	for i := range rules {
		if rules[i].Table == constants.RouteAgentHostNetworkTableID && rules[i].Dst != nil {
			currentRuleRemotes.Insert(rules[i].Dst.String())
		}
	}

	return currentRuleRemotes, nil
}

func (ovn *Handler) programRulesForRemoteSubnets(subnets []string, ruleFunc func(rule *netlink.Rule) error,
	ignoredErrorFunc func(error) bool,
) error {
	for _, remoteSubnet := range subnets {
		rule, err := ovn.getRuleSpec(remoteSubnet, "", constants.RouteAgentHostNetworkTableID)
		if err != nil {
			return errors.Wrapf(err, "error creating rule %#v", rule)
		}

		err = ruleFunc(rule)
		if err != nil && !ignoredErrorFunc(err) {
			return errors.Wrapf(err, "error handling rule %#v", rule)
		}
	}

	return nil
}

func (ovn *Handler) getNextHopOnK8sMgmtIntf() (*net.IP, error) {
	link, err := ovn.netLink.LinkByName(OVNK8sMgmntIntfName)

	if err != nil && !errors.Is(err, netlink.LinkNotFoundError{}) {
		return nil, errors.Wrapf(err, "error retrieving link by name %q", OVNK8sMgmntIntfName)
	}

	currentRouteList, err := ovn.netLink.RouteList(link, syscall.AF_INET)
	if err != nil {
		return nil, errors.Wrapf(err, "error retrieving routes on the link %s", OVNK8sMgmntIntfName)
	}

	for i := range currentRouteList {
		logger.V(log.DEBUG).Infof("Processing route %v", currentRouteList[i])

		if currentRouteList[i].Dst == nil || currentRouteList[i].Gw == nil {
			continue
		}

		// To support hostNetworking use-case the route-agent handler programs default route in table 150
		// with nexthop matching the nexthop on the ovn-k8s-mp0 interface. Basically, we want the Submariner
		// managed traffic to be forwarded to the ovn_cluster_router and pass through the CNI network so that
		// it reaches the active gateway node in the cluster via the submariner pipeline.
		for _, subnet := range ovn.ClusterCIDR {
			if currentRouteList[i].Dst.String() == subnet {
				return &currentRouteList[i].Gw, nil
			}
		}
	}

	return nil, fmt.Errorf("could not find the route to %v via %q", ovn.ClusterCIDR, OVNK8sMgmntIntfName)
}
