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
		return fmt.Errorf("error initializing iptables: %v", err)
	}

	klog.V(4).Infof("Install/ensure %s chain exists", SmPostRoutingChain)
	if err = r.createChainIfNotExists(ipt, "nat", SmPostRoutingChain); err != nil {
		return fmt.Errorf("Unable to create %s chain in iptables: %v", SmPostRoutingChain, err)
	}

	klog.V(4).Infof("Insert %s rule that has rules for inter-cluster traffic", SmPostRoutingChain)
	forwardToSubPostroutingRuleSpec := []string{"-j", SmPostRoutingChain}
	if err = r.prependUnique(ipt, "nat", "POSTROUTING", forwardToSubPostroutingRuleSpec); err != nil {
		klog.Errorf("Unable to insert iptable rule in NAT table, POSTROUTING chain: %v", err)
	}

	klog.V(4).Infof("Install/ensure SUBMARINER-INPUT chain exists")
	if err = r.createChainIfNotExists(ipt, "filter", "SUBMARINER-INPUT"); err != nil {
		return fmt.Errorf("Unable to create SUBMARINER-INPUT chain in iptables: %v", err)
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
	if err = r.prependUnique(ipt, "filter", "FORWARD", ruleSpec); err != nil {
		klog.Errorf("Unable to insert iptable rule in filter table to allow vxlan traffic: %v", err)
	}

	return nil
}

func (r *Controller) programIptableRulesForInterClusterTraffic(remoteCidrBlock string) error {
	ipt, err := iptables.New()
	if err != nil {
		return fmt.Errorf("error initializing iptables: %v", err)
	}

	for _, localClusterCidr := range r.localClusterCidr {
		ruleSpec := []string{"-s", localClusterCidr, "-d", remoteCidrBlock, "-j", "ACCEPT"}
		klog.V(4).Infof("Installing iptables rule for outgoing traffic: %s", strings.Join(ruleSpec, " "))
		if err = ipt.AppendUnique("nat", SmPostRoutingChain, ruleSpec...); err != nil {
			return fmt.Errorf("error appending iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
		}

		// TODO: revisit, we only have to program rules to allow traffic from the podCidr
		ruleSpec = []string{"-s", remoteCidrBlock, "-d", localClusterCidr, "-j", "ACCEPT"}
		klog.V(4).Infof("Installing iptables rule for incoming traffic: %s", strings.Join(ruleSpec, " "))
		if err = ipt.AppendUnique("nat", SmPostRoutingChain, ruleSpec...); err != nil {
			return fmt.Errorf("error appending iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
		}
	}
	return nil
}

func (r *Controller) prependUnique(ipt *iptables.IPTables, table string, chain string, ruleSpec []string) error {
	rules, err := ipt.List(table, chain)
	if err != nil {
		return fmt.Errorf("error listing the rules in %s chain: %v", chain, err)
	}

	// Submariner requires certain iptable rules to be programmed at the beginning of an iptables Chain
	// so that we can preserve the sourceIP for inter-cluster traffic and avoid K8s SDN making changes
	// to the traffic.
	// In this API, we check if the required iptable rule is present at the beginning of the chain.
	// If the rule is already present and there are no stale[1] flows, we simply return. If not, we create one.
	// [1] Sometimes after we program the rule at the beginning of the chain, K8s SDN might insert some
	// new rules ahead of the rule that we programmed. In such cases, the rule that we programmed will
	// not be the first rule to hit and Submariner behavior might get affected. So, we query the rules
	// in the chain to see if the rule slipped its position, and if so, delete all such occurrences.
	// We then re-program a new rule at the beginning of the chain as required.

	isPresentAtRequiredPosition := false
	numOccurrences := 0
	for index, rule := range rules {
		if strings.Contains(rule, strings.Join(ruleSpec, " ")) {
			klog.V(4).Infof("In %s table, iptables rule \"%s\", exists at index %d.", table, strings.Join(ruleSpec, " "), index)
			numOccurrences++

			if index == 1 {
				isPresentAtRequiredPosition = true
			}
		}
	}

	// The required rule is present in the Chain, but either there are multiple occurrences or its
	// not at the desired location
	if numOccurrences > 1 || !isPresentAtRequiredPosition {
		for i := 0; i < numOccurrences; i++ {
			if err = ipt.Delete(table, chain, ruleSpec...); err != nil {
				return fmt.Errorf("error deleting stale iptable rule \"%s\": %v", strings.Join(ruleSpec, " "), err)
			}
		}
	}

	// The required rule is present only once and is at the desired location
	if numOccurrences == 1 && isPresentAtRequiredPosition {
		klog.V(4).Infof("In %s table, iptables rule \"%s\", already exists.", table, strings.Join(ruleSpec, " "))
		return nil
	} else {
		if err = ipt.Insert(table, chain, 1, ruleSpec...); err != nil {
			return err
		}
	}

	return nil
}

func (r *Controller) createChainIfNotExists(ipt *iptables.IPTables, table, chain string) error {
	existingChains, err := ipt.ListChains(table)
	if err != nil {
		return err
	}

	for _, val := range existingChains {
		if val == chain {
			// Chain already exists
			return nil
		}
	}

	return ipt.NewChain(table, chain)
}
