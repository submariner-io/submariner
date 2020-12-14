package ovn

import (
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/coreos/go-iptables/iptables"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/stringset"
	"github.com/vishvananda/netlink"
	"k8s.io/klog"

	npSyncerOvn "github.com/submariner-io/submariner/pkg/networkplugin-syncer/handlers/ovn"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	iptcommon "github.com/submariner-io/submariner/pkg/routeagent_driver/iptables"
	"github.com/submariner-io/submariner/pkg/util"
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

	for _, handler := range ovn.cleanupHandlers {
		// TODO: Make those handlers have a single cleanup on GatewayToNonGateway transition,
		//       we can make them a normal event handler, instead of a cleanup handler, but
		//       we need to make sure that TransitionToNonGateway is fired at least one for
		//       a controller which is started once and no endpoint is received on for current
		//       host.
		if err := handler.NonGatewayCleanup(); err != nil {
			return errors.Wrap(err, "NonGatewayCleanup")
		} else if err := handler.GatewayToNonGatewayTransition(); err != nil {
			return errors.Wrap(err, "GatewayToNonGatewayTransition")
		}
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

	if err = ovn.updateNoMasqueradeIpTables(); err != nil {
		return errors.Wrap(err, "error handling no-masquerade rules")
	}

	return ovn.setupForwardingIptables()
}

// TODO: if the #1022 workaround needs to be sustained for some time, instead of this we should be calculating
//       the PMTU with a tool like tracepath between the gateway endpoints, reporting back so we can use such
//		 information here.
const IPTCPOverHead = 40
const ExpectedIPSECOverhead = 62
const MSSFor1500MTU = 1500 - IPTCPOverHead - ExpectedIPSECOverhead

func (ovn *Handler) getForwardingRuleSpecs() ([][]string, error) {
	if ovn.cableRoutingInterface == nil {
		return nil, errors.New("error setting up forwarding iptables, the cable interface isn't discovered yet, " +
			"this will be retried")
	}

	rules := [][]string{
		{"-i", ovnK8sGatewayInterface, "-o", ovn.cableRoutingInterface.Name, "-j", "ACCEPT"},
		{"-i", ovn.cableRoutingInterface.Name, "-o", ovnK8sGatewayInterface, "-j", "ACCEPT"}}

	// NOTE: This is a workaround for submariner issue https://github.com/submariner-io/submariner/issues/1022
	for _, serviceCIDR := range ovn.config.ServiceCidr {
		rules = append(rules, []string{"-o", ovnK8sGatewayInterface, "-d", serviceCIDR, "-p", "tcp",
			"--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS", "--set-mss", strconv.Itoa(MSSFor1500MTU)})
	}

	return rules, nil
}

func (ovn *Handler) setupForwardingIptables() error {
	ipt, err := iptables.New()
	if err != nil {
		return errors.Wrap(err, "error initializing iptables")
	}

	ruleSpecs, err := ovn.getForwardingRuleSpecs()
	if err != nil {
		return err
	}

	for _, ruleSpec := range ruleSpecs {
		if err = util.PrependUnique(ipt, "filter", "FORWARD", ruleSpec); err != nil {
			return errors.Wrap(err, "unable to insert iptable rule in filter table to forward submariner traffic")
		}
	}

	return nil
}

func (ovn *Handler) updateNoMasqueradeIpTables() error {
	ipt, err := iptables.New()
	if err != nil {
		return errors.Wrap(err, "error initializing iptables")
	}

	rules, err := ipt.List("nat", constants.SmPostRoutingChain)
	if err != nil {
		return errors.Wrapf(err, "error listing the rules in %s chain", constants.SmPostRoutingChain)
	}

	ruleStrings := stringset.New()

	for _, rule := range rules {
		ruleSpec := strings.Split(rule, " ")
		if ruleSpec[0] == "-A" {
			ruleSpec = ruleSpec[2:] // remove "-A", "SUBMARINER-POSTROUTING"
			ruleStrings.Add(strings.Trim(strings.Join(ruleSpec, " "), " "))
		}
	}

	for _, ruleSpec := range ovn.getNoMasqueradRuleSpecs() {
		ruleString := strings.Join(ruleSpec, " ")
		if ruleStrings.Contains(ruleString) {
			ruleStrings.Remove(ruleString)
		} else {
			klog.Infof("Adding iptables rule in %q: %v", constants.SmPostRoutingChain, ruleSpec)

			if err := ipt.Append("nat", constants.SmPostRoutingChain, ruleSpec...); err != nil {
				return errors.Wrapf(err, "error adding remote cluster rule to %v to %q", ruleSpec, constants.SmPostRoutingChain)
			}
		}
	}

	// remaining elements should not be there, remove them
	for _, rule := range ruleStrings.Elements() {
		klog.Infof("Deleting stale iptables rule in %q: %q", constants.SmPostRoutingChain, rule)
		ruleSpec := strings.Split(rule, " ")

		if err := ipt.Delete("nat", constants.SmPostRoutingChain, ruleSpec...); err != nil {
			// Log and let go, as this is not a fatal error, or something that will make real harm,
			// it's more harmful to keep retrying. At this point on next update deletion of stale rules
			// will happen again
			klog.Warningf("Unable to delete iptables entry from %q: %q", constants.SmPostRoutingChain, rule)
		}
	}

	return nil
}

func (ovn *Handler) getNoMasqueradRuleSpecs() [][]string {
	var rules [][]string

	for _, endpoint := range ovn.remoteEndpoints {
		for _, subnet := range endpoint.Spec.Subnets {
			rules = append(rules, []string{"-d", subnet, "-j", "ACCEPT"})
		}
	}

	return rules
}

func (ovn *Handler) cleanupForwardingIptables() error {
	ipt, err := iptables.New()
	if err != nil {
		return errors.Wrap(err, "error initializing iptables")
	}

	ruleSpecs, err := ovn.getForwardingRuleSpecs()
	if err != nil {
		return err
	}

	for _, ruleSpec := range ruleSpecs {
		err = ipt.Delete("filter", "FORWARD", ruleSpec...)
		if err != nil {
			// We log, and don't return, because there could be some transient errors on delete if the
			// rule didn't exist, we don't want to retry
			klog.Errorf("error cleaning FORWARD tables: %s", err)
		}
	}

	return nil
}

func (ovn *Handler) getSubmDefaultRoute() *netlink.Route {
	submarinerUpstreamIP, err := ovn.getSubmarinerUpstreamIP()

	// This is a non-retriable error, if this happens we want a hard &
	// visible error: the route-agent pod exiting with error
	if err != nil {
		klog.Fatal(err)
	}

	return &netlink.Route{
		Gw:    net.ParseIP(submarinerUpstreamIP),
		Table: constants.RouteAgentInterClusterNetworkTableID,
	}
}

func (ovn *Handler) getSubmarinerUpstreamIP() (string, error) {
	if ovn.submarinerUpstreamIP != "" {
		return ovn.submarinerUpstreamIP, nil
	}

	iface, err := net.InterfaceByName(ovnK8sGatewayInterface)
	if err != nil {
		return "", errors.Wrapf(err, "error looking for %q interface, trying to detect the submariner_router upstream IP",
			ovnK8sGatewayInterface)
	}

	addresses, err := iface.Addrs()
	if err != nil {
		return "", errors.Wrapf(err, "error listing %q interface addresses while trying to detect the submariner_router upstream IP",
			ovnK8sGatewayInterface)
	}

	for _, addr := range addresses {
		switch {
		case strings.HasPrefix(addr.String(), "169.254.0."):
			ovn.submarinerUpstreamIP = npSyncerOvn.SubmarinerUpstreamIPv2
		case strings.HasPrefix(addr.String(), "169.254.33."):
			ovn.submarinerUpstreamIP = npSyncerOvn.SubmarinerUpstreamIP
		}
	}

	if ovn.submarinerUpstreamIP == "" {
		return "", errors.Errorf("Unable to figure out the submariner_router upstream IP address based on %q addresses %#v",
			ovnK8sGatewayInterface, addresses)
	}

	klog.Infof("Submariner router upstream ip found to be: %q", ovn.submarinerUpstreamIP)

	return ovn.submarinerUpstreamIP, nil
}

func (ovn *Handler) initIPtablesChains() error {
	ipt, err := iptables.New()
	if err != nil {
		return errors.Wrapf(err, "error initializing iptables")
	}

	if err := iptcommon.InitSubmarinerPostRoutingChain(ipt); err != nil {
		return err
	}

	return nil
}
