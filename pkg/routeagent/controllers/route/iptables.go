package route

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/coreos/go-iptables/iptables"
	"k8s.io/klog"
)

func (r *Controller) createIPTableChains() error {
	ipt, err := iptables.New()
	if err != nil {
		klog.Errorf("Error while initializing iptables: %v", err)
		return err
	}

	klog.V(4).Infof("Install/ensure %s chain exists", SmPostRoutingChain)
	if err = ipt.NewChain("nat", SmPostRoutingChain); err != nil {
		klog.Errorf("Unable to create %s chain in iptables: %v", SmPostRoutingChain, err)
	}

	klog.V(4).Infof("Insert %s rule that has rules for inter-cluster traffic", SmPostRoutingChain)
	forwardToSubPostroutingRuleSpec := []string{"-j", SmPostRoutingChain}
	if err = r.insertUnique(ipt, "nat", "POSTROUTING", 1, forwardToSubPostroutingRuleSpec); err != nil {
		klog.Errorf("Unable to insert iptable rule in NAT table, POSTROUTING chain: %v", err)
	}

	klog.V(4).Infof("Install/ensure SUBMARINER-INPUT chain exists")
	if err = ipt.NewChain("filter", "SUBMARINER-INPUT"); err != nil {
		klog.Errorf("Unable to create SUBMARINER-INPUT chain in iptables: %v", err)
	}

	forwardToSubInputRuleSpec := []string{"-p", "udp", "-m", "udp", "-j", "SUBMARINER-INPUT"}
	if err = ipt.AppendUnique("filter", "INPUT", forwardToSubInputRuleSpec...); err != nil {
		klog.Errorf("Unable to append iptables rule \"%s\": %v\n", strings.Join(forwardToSubInputRuleSpec, " "), err)
	}

	klog.V(4).Infof("Allow VxLAN incoming traffic in SUBMARINER-INPUT Chain")
	ruleSpec := []string{"-p", "udp", "-m", "udp", "--dport", strconv.Itoa(VxLANPort), "-j", "ACCEPT"}
	if err = ipt.AppendUnique("filter", "SUBMARINER-INPUT", ruleSpec...); err != nil {
		klog.Errorf("Unable to append iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
	}

	klog.V(4).Infof("Insert rule to allow traffic over %s interface in FORWARDing Chain", VxLANIface)
	ruleSpec = []string{"-o", VxLANIface, "-j", "ACCEPT"}
	if err = r.insertUnique(ipt, "filter", "FORWARD", 1, ruleSpec); err != nil {
		klog.Errorf("Unable to insert iptable rule in filter table to allow vxlan traffic: %v", err)
	}

	return nil
}

func (r *Controller) programIptableRulesForInterClusterTraffic(remoteCidrBlock string) {
	ipt, err := iptables.New()
	if err != nil {
		klog.Errorf("error while initializing iptables: %v", err)
	}

	for _, localClusterCidr := range r.localClusterCidr {
		ruleSpec := []string{"-s", localClusterCidr, "-d", remoteCidrBlock, "-j", "ACCEPT"}
		klog.V(4).Infof("Installing iptables rule for outgoing traffic: %s", strings.Join(ruleSpec, " "))
		if err = ipt.AppendUnique("nat", SmPostRoutingChain, ruleSpec...); err != nil {
			klog.Errorf("error appending iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
		}

		// Todo: revisit, we only have to program rules to allow traffic from the podCidr
		ruleSpec = []string{"-s", remoteCidrBlock, "-d", localClusterCidr, "-j", "ACCEPT"}
		klog.V(4).Infof("Installing iptables rule for incoming traffic: %s", strings.Join(ruleSpec, " "))
		if err = ipt.AppendUnique("nat", SmPostRoutingChain, ruleSpec...); err != nil {
			klog.Errorf("error appending iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
		}
	}
}

func (r *Controller) insertUnique(ipt *iptables.IPTables, table string, chain string, position int, ruleSpec []string) error {
	rules, err := ipt.List(table, chain)
	if err != nil {
		return fmt.Errorf("error listing the rules in %s chain: %v", chain, err)
	}

	if strings.Contains(rules[position], strings.Join(ruleSpec, " ")) {
		klog.V(4).Infof("In %s table, iptables rule \"%s\", already exists.", table, strings.Join(ruleSpec, " "))
		return nil
	} else {
		if err = ipt.Insert(table, chain, position, ruleSpec...); err != nil {
			klog.Errorf("In %s table, unable to insert iptables rule \"%s\": %v\n", table, strings.Join(ruleSpec, " "), err)
		}
	}
	return nil
}
