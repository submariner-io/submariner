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
	"github.com/submariner-io/admiral/pkg/stringset"
	"github.com/vishvananda/netlink"

	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
)

// handleSubnets builds ip rules, and passes them to the specified netlink function
//               for provided subnet list
func (ovn *Handler) handleSubnets(subnets []string, ruleFunc func(rule *netlink.Rule) error,
	ignoredErrorFunc func(error) bool) error {
	for _, subnetToHandle := range subnets {
		for _, localSubnet := range ovn.localEndpoint.Spec.Subnets {
			rule, err := ovn.programRule(localSubnet, subnetToHandle, constants.RouteAgentInterClusterNetworkTableID)
			if err != nil {
				return errors.Wrapf(err, "error creating rule %#v", rule)
			}

			err = ruleFunc(rule)
			if err != nil && !ignoredErrorFunc(err) {
				return errors.Wrapf(err, "error handling rule %#v", rule)
			}
		}
	}

	return nil
}

func (ovn *Handler) programRule(dest, src string, tableID int) (*netlink.Rule, error) {
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

func (ovn *Handler) getExistingIPv4RuleSubnets() (stringset.Interface, error) {
	currentRuleRemotes := stringset.New()
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return nil, err
	}

	for _, rule := range rules {
		if rule.Table == constants.RouteAgentInterClusterNetworkTableID && rule.Src != nil {
			currentRuleRemotes.Add(rule.Src.String())
		}
	}

	return currentRuleRemotes, nil
}
