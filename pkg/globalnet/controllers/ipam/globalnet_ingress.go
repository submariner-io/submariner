package ipam

import (
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"

	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/util"
)

func (i *Controller) initIPTableChains() error {
	klog.V(4).Infof("Install/ensure %s chain exists", submarinerGlobalNet)
	if err := util.CreateChainIfNotExists(i.ipt, "nat", submarinerGlobalNet); err != nil {
		return fmt.Errorf("error creating iptables chain %s: %v", submarinerGlobalNet, err)
	}

	forwardToSubGlobalNetRuleSpec := []string{"-j", submarinerGlobalNet}
	if err := util.PrependUnique(i.ipt, "nat", "PREROUTING", forwardToSubGlobalNetRuleSpec); err != nil {
		klog.Errorf("error appending iptables rule \"%s\": %v\n", strings.Join(forwardToSubGlobalNetRuleSpec, " "), err)
	}
	return nil
}

func (i *Controller) updateIngressRulesForService(globalIP, chainName string, addOrDelete bool) error {
	ruleSpec := []string{"-d", globalIP, "-j", chainName}
	if addOrDelete == AddRules {
		klog.V(4).Infof("Installing iptables rule for Service %s", strings.Join(ruleSpec, " "))
		if err := i.ipt.AppendUnique("nat", submarinerGlobalNet, ruleSpec...); err != nil {
			return fmt.Errorf("error appending iptables rule \"%s\": %v\n", strings.Join(ruleSpec, " "), err)
		}
	} else if addOrDelete == DeleteRules {
		klog.V(4).Infof("Deleting iptable ingress rule for Service: %s", strings.Join(ruleSpec, " "))
		if err := i.ipt.Delete("nat", submarinerGlobalNet, ruleSpec...); err != nil {
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

func (i *Controller) doesIPTablesChainExist(table, chain string) error {
	existingChains, err := i.ipt.ListChains(table)
	if err != nil {
		return err
	}

	for _, val := range existingChains {
		if val == chain {
			klog.V(4).Infof("%s chain exists in %s table", chain, table)
			return nil
		}
	}
	return fmt.Errorf("chain %s does not exist", chain)
}
