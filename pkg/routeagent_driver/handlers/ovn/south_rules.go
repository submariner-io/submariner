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
			rule, err := ovn.ruleForSouthTraffic(localSubnet, subnetToHandle)
			if err != nil {
				return errors.Wrapf(err, "creating rule %#v", rule)
			}

			err = ruleFunc(rule)
			if err != nil && !ignoredErrorFunc(err) {
				return errors.Wrapf(err, "handling rule %#v with dst=%q src=%q", rule, rule.Dst.String(), rule.Src.String())
			}
		}
	}

	return nil
}

func (ovn *Handler) ruleForSouthTraffic(localSubnet, remoteSubnet string) (*netlink.Rule, error) {
	_, dstCIDR, err := net.ParseCIDR(localSubnet)
	if err != nil {
		return nil, errors.Wrapf(err, "error trying to parse local subnet %q", localSubnet)
	}

	_, srcCIDR, err := net.ParseCIDR(remoteSubnet)
	if err != nil {
		return nil, errors.Wrapf(err, "error trying to parse remote subnet %q", remoteSubnet)
	}

	rule := netlink.NewRule()
	rule.Dst = dstCIDR
	rule.Src = srcCIDR
	rule.Table = constants.RouteAgentHostNetworkTableID

	return rule, nil
}

func (ovn *Handler) getExistingRuleSubnets() (stringset.Interface, error) {
	currentRuleRemotes := stringset.New()
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return nil, err
	}

	for _, rule := range rules {
		if rule.Table == constants.RouteAgentHostNetworkTableID && rule.Src != nil {
			currentRuleRemotes.Add(rule.Src.String())
		}
	}

	return currentRuleRemotes, nil
}
