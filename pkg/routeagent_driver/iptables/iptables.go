package iptables

import (
	"github.com/coreos/go-iptables/iptables"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/util"
)

func InitSubmarinerPostRoutingChain(ipt *iptables.IPTables) error {
	klog.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmPostRoutingChain)

	if err := util.CreateChainIfNotExists(ipt, "nat", constants.SmPostRoutingChain); err != nil {
		return errors.Wrapf(err, "unable to create %q chain in iptables", constants.SmPostRoutingChain)
	}

	klog.V(log.DEBUG).Infof("Insert %s rule that has rules for inter-cluster traffic", constants.SmPostRoutingChain)

	forwardToSubPostroutingRuleSpec := []string{"-j", constants.SmPostRoutingChain}

	if err := util.PrependUnique(ipt, "nat", "POSTROUTING", forwardToSubPostroutingRuleSpec); err != nil {
		return errors.Wrapf(err, "unable to insert iptable rule in NAT table, POSTROUTING chain")
	}

	return nil
}
