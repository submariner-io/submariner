package ipam

import (
	"fmt"
	"strings"

	"github.com/coreos/go-iptables/iptables"
	"k8s.io/klog"
)

func (i *Controller) updateEgressRulesForPod(podIP, globalIP string, addRules bool) error {
	ruleSpec := []string{"-p", "all", "-s", podIP, "-m", "mark", "--mark", globalNetIPTableMark, "-j", "SNAT", "--to", globalIP}
	if addRules {
		klog.V(4).Infof("Installing iptable egress rules for pod: %s", strings.Join(ruleSpec, " "))
		if err := i.ipt.AppendUnique("nat", submarinerEgress, ruleSpec...); err != nil {
			return fmt.Errorf("error appending iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
		}
	} else {
		klog.V(4).Infof("Deleting iptable egress rules for pod: %s", strings.Join(ruleSpec, " "))
		if err := i.ipt.Delete("nat", submarinerEgress, ruleSpec...); err != nil {
			return fmt.Errorf("error deleting iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
		}
	}
	return nil
}

func MarkRemoteClusterTraffic(ipt *iptables.IPTables, remoteCidr string, addRules bool) {
	ruleSpec := []string{"-d", remoteCidr, "-j", "MARK", "--set-mark", globalNetIPTableMark}
	if addRules {
		klog.V(4).Infof("Marking traffic destined to remote cluster: %s", strings.Join(ruleSpec, " "))
		if err := ipt.AppendUnique("nat", submarinerMark, ruleSpec...); err != nil {
			klog.Errorf("error appending iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
		}
	} else {
		klog.V(4).Infof("Deleting rule that marks remote cluster traffic: %s", strings.Join(ruleSpec, " "))
		if err := ipt.Delete("nat", submarinerMark, ruleSpec...); err != nil {
			klog.Errorf("error deleting iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
		}
	}
}
