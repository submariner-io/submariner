package ipam

import (
	"fmt"
	"strings"

	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/routeagent/controllers/route"
)

func (i *Controller) updateEgressRulesForPod(podIP, globalIP string, addOrDelete bool) error {
	ruleSpec := []string{"-p", "all", "-s", podIP, "-j", "SNAT", "--to", globalIP}
	if addOrDelete == AddRules {
		klog.V(4).Infof("Installing iptable egress rules for pod: %s", strings.Join(ruleSpec, " "))
		if err := i.ipt.AppendUnique("nat", route.SmPostRoutingChain, ruleSpec...); err != nil {
			return fmt.Errorf("error appending iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
		}
	} else if addOrDelete == DeleteRules {
		klog.V(4).Infof("Deleting iptable egress rules for pod: %s", strings.Join(ruleSpec, " "))
		if err := i.ipt.Delete("nat", route.SmPostRoutingChain, ruleSpec...); err != nil {
			return fmt.Errorf("error deleting iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
		}
	}
	return nil
}
