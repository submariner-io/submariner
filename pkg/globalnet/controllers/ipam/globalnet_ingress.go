package ipam

import (
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"

	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/klog"
)

func (i *Controller) updateIngressRulesForService(globalIP, chainName string, addRules bool) error {
	ruleSpec := []string{"-d", globalIP, "-j", chainName}
	if addRules {
		klog.V(4).Infof("Installing iptables rule for Service %s", strings.Join(ruleSpec, " "))
		if err := i.ipt.AppendUnique("nat", submarinerIngress, ruleSpec...); err != nil {
			return fmt.Errorf("error appending iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
		}
	} else {
		klog.V(4).Infof("Deleting iptable ingress rule for Service: %s", strings.Join(ruleSpec, " "))
		if err := i.ipt.Delete("nat", submarinerIngress, ruleSpec...); err != nil {
			return fmt.Errorf("error deleting iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
		}
	}
	return nil
}

func (i *Controller) kubeProxyClusterIpServiceChainName(service *k8sv1.Service) string {
	// CNIs that use kube-proxy with iptables for loadbalancing create an iptables chain for each service
	// and incoming traffic to the clusterIP Service is directed into the respective chain.
	// Reference: https://bit.ly/2OPhlwk
	serviceName := service.GetNamespace() + "/" + service.GetName() + ":"
	protocol := strings.ToLower(string(service.Spec.Ports[0].Protocol))
	hash := sha256.Sum256([]byte(serviceName + protocol))
	encoded := base32.StdEncoding.EncodeToString(hash[:])
	return kubeProxyServiceChainPrefix + encoded[:16]
}

func (i *Controller) doesIPTablesChainExist(table, chain string) (bool, error) {
	existingChains, err := i.ipt.ListChains(table)
	if err != nil {
		klog.V(4).Infof("Error listing iptables chains in %s table: %s", table, err)
		return false, err
	}

	for _, val := range existingChains {
		if val == chain {
			klog.V(4).Infof("%s chain exists in %s table", chain, table)
			return true, nil
		}
	}
	return false, nil
}
