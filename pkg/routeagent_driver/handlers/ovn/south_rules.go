package ovn

import (
	"net"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/stringset"
	"github.com/vishvananda/netlink"
)

const ovnRoutingRulesTable = 5

// handleSubnets builds ip rules, and passes them to the specified netlink function
//               for provided subnet list
func (ovn *Handler) handleSubnets(subnets []string, ruleFunc func(rule *netlink.Rule) error,
	ignoredErrorFunc func(error) bool, event string) error {
	for _, subnetToHandle := range subnets {
		for _, localSubnet := range ovn.localEndpoint.Spec.Subnets {
			rule, err := ovn.ruleForSouthTraffic(localSubnet, subnetToHandle, event)
			if err != nil {
				return err
			}

			err = ruleFunc(rule)
			if err != nil && !ignoredErrorFunc(err) {
				return err
			}
		}
	}

	return nil
}

func (ovn *Handler) ruleForSouthTraffic(localSubnet, remoteSubnet, event string) (*netlink.Rule, error) {
	_, dstCIDR, err := net.ParseCIDR(localSubnet)
	if err != nil {
		return nil, errors.Wrapf(err, "error trying to parse local subnet %q on %q", localSubnet, event)
	}

	_, srcCIDR, err := net.ParseCIDR(remoteSubnet)
	if err != nil {
		return nil, errors.Wrapf(err, "error trying to parse remote subnet %q on %q", remoteSubnet, event)
	}

	return &netlink.Rule{
		Dst:   dstCIDR,
		Src:   srcCIDR,
		Table: ovnRoutingRulesTable}, nil
}

func (ovn *Handler) getExistingRuleSubnets() (stringset.Interface, error) {
	currentRuleRemotes := stringset.New()
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return nil, err
	}

	for _, rule := range rules {
		if rule.Table == ovnRoutingRulesTable && rule.Src != nil {
			currentRuleRemotes.Add(rule.Src.String())
		}
	}

	return currentRuleRemotes, nil
}
