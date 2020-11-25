package ovn

import (
	"fmt"
	"net"
	"os"

	"github.com/coreos/go-iptables/iptables"
	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"

	"github.com/submariner-io/submariner/pkg/util"
)

func (ovn *Handler) cleanupGatewayDataplane() error {
	currentRemoteSubnets, err := ovn.getExistingRuleSubnets()
	if err != nil {
		return errors.Wrapf(err, "error reading ip rule list for IPv4 in TransitionToNonGateway")
	}

	err = ovn.handleSubnets(currentRemoteSubnets.Elements(), netlink.RuleDel, os.IsNotExist, "TransitionToNonGateway")
	if err != nil {
		return errors.Wrapf(err, "error removing routing rule")
	}

	err = netlink.RouteDel(getSubmDefaultRoute())
	if err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "error deleting submariner default route during TransitionToNonGateway")
	}

	return ovn.cleanupForwardingIptables()
}

func (ovn *Handler) updateGatewayDataplane() error {
	currentRuleRemotes, err := ovn.getExistingRuleSubnets()
	if err != nil {
		return errors.Wrapf(err, "error reading ip rule list for IPv4 in TransitionToGateway")
	}

	endpointSubnets := ovn.getRemoteSubnets()

	toAdd := currentRuleRemotes.Difference(endpointSubnets)

	err = ovn.handleSubnets(toAdd, netlink.RuleAdd, os.IsExist, "TransitionToGateway")
	if err != nil {
		return errors.Wrapf(err, "error adding routing rule")
	}

	toRemove := endpointSubnets.Difference(currentRuleRemotes)

	err = ovn.handleSubnets(toRemove, netlink.RuleDel, os.IsNotExist, "TransitionToGateway")
	if err != nil {
		return errors.Wrapf(err, "error removing routing rule")
	}

	err = netlink.RouteAdd(getSubmDefaultRoute())
	if err != nil && !os.IsExist(err) {
		return errors.Wrap(err, "error adding submariner default route during TransitionToGateway")
	}

	return ovn.setupForwardingIptables()
}

func (ovn *Handler) setupForwardingIptables() error {
	ipt, err := iptables.New()
	if err != nil {
		return fmt.Errorf("error initializing iptables: %v", err)
	}

	ruleSpec := []string{"-i", "ovn-k8s-gw0", "-o", ovn.cableRoutingInterface.Name, "-j", "ACCEPT"}

	if err = util.PrependUnique(ipt, "filter", "FORWARD", ruleSpec); err != nil {
		return fmt.Errorf("unable to insert iptable rule in filter table to forward submariner traffic: %v", err)
	}

	ruleSpec = []string{"-i", ovn.cableRoutingInterface.Name, "-o", "ovn-k8s-gw0", "-j", "ACCEPT"}

	if err = util.PrependUnique(ipt, "filter", "FORWARD", ruleSpec); err != nil {
		return fmt.Errorf("unable to insert iptable rule in filter table to allow vxlan traffic: %v", err)
	}

	return nil
}

func (ovn *Handler) cleanupForwardingIptables() error {
	ipt, err := iptables.New()
	if err != nil {
		return fmt.Errorf("error initializing iptables: %v", err)
	}

	_ = ipt.Delete("filter", "FORWARD", "-i", "ovn-k8s-gw0", "-o", ovn.cableRoutingInterface.Name, "-j", "ACCEPT")
	_ = ipt.Delete("filter", "FORWARD", "-i", ovn.cableRoutingInterface.Name, "-o", "ovn-k8s-gw0", "-j", "ACCEPT")

	return nil
}

// TODO: Use the constant defined in networkplugin-syncer handler when that's merged.
const submarinerUpstreamIP = "169.254.33.7"

func getSubmDefaultRoute() *netlink.Route {
	return &netlink.Route{
		Gw:    net.ParseIP(submarinerUpstreamIP),
		Table: ovnRoutingRulesTable,
	}
}
