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

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/resource"
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

	currentRuleRemotes, err := ovn.getExistingHostNetworkRoutes()
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

func (ovn *Handler) getExistingHostNetworkRoutes() (set.Set[string], error) {
	currentRuleRemotes := set.New[string]()

	rules, err := ovn.netLink.RuleList(ovn.ipFamily)
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
			return errors.Wrapf(err, "error handling rule: %s", resource.ToJSON(rule))
		}
	}

	return nil
}

func (ovn *Handler) getNextHopOnK8sMgmtIntf() (*net.IP, error) {
	link, err := ovn.netLink.LinkByName(OVNK8sMgmntIntfName)
	if err != nil {
		return nil, errors.Wrapf(err, "error retrieving link by name %q", OVNK8sMgmntIntfName)
	}

	routes, err := ovn.netLink.RouteList(link, ovn.ipFamily)
	if err != nil {
		return nil, errors.Wrapf(err, "error retrieving IPv%v routes on link %s", ovn.ipFamily, OVNK8sMgmntIntfName)
	}

	parsedClusterCIDRs := make([]*net.IPNet, 0, len(ovn.ClusterCIDR))

	for _, subnet := range ovn.ClusterCIDR {
		_, cidrNet, err := net.ParseCIDR(subnet)
		if err != nil {
			logger.Error(err, "Failed to parse CIDR", "subnet", subnet)
			continue
		}

		parsedClusterCIDRs = append(parsedClusterCIDRs, cidrNet)
	}

	for i := range routes {
		if routes[i].Dst == nil {
			continue
		}

		// To support hostNetworking use-case the route-agent handler programs default route in table 150
		// with nexthop matching the nexthop on the ovn-k8s-mp0 interface. Basically, we want the Submariner
		// managed traffic to be forwarded to the ovn_cluster_router and pass through the CNI network so that
		// it reaches the active gateway node in the cluster via the submariner pipeline.
		logger.V(log.TRACE).Info("Processing route", "Dst", routes[i].Dst.String(), "Gw", routes[i].Gw.String())

		for _, cidrNet := range parsedClusterCIDRs {
			if routes[i].Dst.String() == cidrNet.String() ||
				cidrNet.Contains(routes[i].Dst.IP) ||
				routes[i].Dst.Contains(cidrNet.IP) {
				logger.V(log.TRACE).Info("Matched route", "Dst", routes[i].Dst.String(), "Gw", routes[i].Gw.String())

				if routes[i].Gw != nil {
					return &routes[i].Gw, nil
				}

				localIP := routes[i].Dst.IP

				return &localIP, nil
			}
		}
	}

	return nil, fmt.Errorf("could not find the route to any of %v via %q", ovn.ClusterCIDR, OVNK8sMgmntIntfName)
}
