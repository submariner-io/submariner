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

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/vishvananda/netlink"
	"k8s.io/utils/set"
)

// handleSubnets builds ip rules, and passes them to the specified netlink function
//
//	for provided subnet list
func (ovn *Handler) handleSubnets(remoteSubnets []string, ruleFunc func(rule *netlink.Rule) error,
	ignoredErrorFunc func(error) bool,
) error {
	localCIDRs := set.New(ovn.ClusterCIDR...)
	localCIDRs.Insert(ovn.ServiceCIDR...)

	for _, subnetToHandle := range remoteSubnets {
		for _, localSubnet := range localCIDRs.UnsortedList() {
			rule, err := ovn.getRuleSpec(localSubnet, subnetToHandle, constants.RouteAgentInterClusterNetworkTableID)
			if err != nil {
				return errors.Wrapf(err, "error creating rule %#v", rule)
			}

			logger.V(log.DEBUG).Infof("Adding routes in table 149: %v", rule)

			err = ruleFunc(rule)
			if err != nil && !ignoredErrorFunc(err) {
				return errors.Wrapf(err, "error handling rule %#v", rule)
			}
		}
	}

	return nil
}

func (ovn *Handler) getRuleSpec(dest, src string, tableID int) (*netlink.Rule, error) {
	rule := netlink.NewRule()

	if dest != "" {
		_, dstCIDR, err := net.ParseCIDR(dest)
		if err != nil {
			return nil, errors.Wrapf(err, "error trying to parse toSubnet %q", dest)
		}

		rule.Dst = dstCIDR
	}

	if src != "" {
		_, srcCIDR, err := net.ParseCIDR(src)
		if err != nil {
			return nil, errors.Wrapf(err, "error trying to parse fromSubnet %q", src)
		}

		rule.Src = srcCIDR
	}

	rule.Table = tableID
	rule.Priority = tableID

	return rule, nil
}

func (ovn *Handler) getExistingIPv4RuleSubnets() (set.Set[string], error) {
	currentRuleRemotes := set.New[string]()

	rules, err := ovn.netLink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return nil, errors.Wrapf(err, "error listing rules")
	}

	for i := range rules {
		if rules[i].Table == constants.RouteAgentInterClusterNetworkTableID && rules[i].Src != nil {
			currentRuleRemotes.Insert(rules[i].Src.String())
		}
	}

	return currentRuleRemotes, nil
}
