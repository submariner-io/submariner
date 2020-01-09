package ipam

import (
	"fmt"
	"strings"

	"k8s.io/klog"
)

func (i *Controller) updateEgressRulesForPod(podIP, globalIP string, addRules bool) error {
	ruleSpec := []string{"-p", "all", "-s", podIP, "-j", "SNAT", "--to", globalIP}
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
