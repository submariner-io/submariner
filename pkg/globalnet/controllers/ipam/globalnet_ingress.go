/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package ipam

import (
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"

	"github.com/submariner-io/admiral/pkg/log"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/globalnet/constants"
)

func (i *Controller) updateIngressRulesForService(globalIP, chainName string, addRules bool) error {
	ruleSpec := []string{"-d", globalIP, "-j", chainName}

	if addRules {
		klog.V(log.DEBUG).Infof("Installing iptables rule for Service %s", strings.Join(ruleSpec, " "))

		if err := i.ipt.AppendUnique("nat", constants.SmGlobalnetIngressChain, ruleSpec...); err != nil {
			return fmt.Errorf("error appending iptables rule \"%s\": %v", strings.Join(ruleSpec, " "), err)
		}
	} else {
		klog.V(log.DEBUG).Infof("Deleting iptable ingress rule for Service: %s", strings.Join(ruleSpec, " "))

		if err := i.ipt.Delete("nat", constants.SmGlobalnetIngressChain, ruleSpec...); err != nil {
			return fmt.Errorf("error deleting iptables rule \"%s\": %v", strings.Join(ruleSpec, " "), err)
		}
	}

	return nil
}

func (i *Controller) kubeProxyClusterIPServiceChainName(service *k8sv1.Service) (string, bool, error) {
	// CNIs that use kube-proxy with iptables for loadbalancing create an iptables chain for each service
	// and incoming traffic to the clusterIP Service is directed into the respective chain.
	// Reference: https://bit.ly/2OPhlwk
	prefix := service.GetNamespace() + "/" + service.GetName()
	serviceNames := []string{prefix + ":" + service.Spec.Ports[0].Name}

	if service.Spec.Ports[0].Name == "" {
		// In newer k8s versions (v1.19+), they omit the ":" if the port name is empty so we need to handle both formats (see
		// https://github.com/kubernetes/kubernetes/pull/90031).
		serviceNames = append(serviceNames, prefix)
	}

	for _, serviceName := range serviceNames {
		protocol := strings.ToLower(string(service.Spec.Ports[0].Protocol))
		hash := sha256.Sum256([]byte(serviceName + protocol))
		encoded := base32.StdEncoding.EncodeToString(hash[:])
		chainName := kubeProxyServiceChainPrefix + encoded[:16]

		chainExists, err := i.doesIPTablesChainExist("nat", chainName)
		if err != nil {
			return "", false, err
		}

		if chainExists {
			return chainName, true, nil
		}
	}

	return "", false, nil
}

func (i *Controller) doesIPTablesChainExist(table, chain string) (bool, error) {
	existingChains, err := i.ipt.ListChains(table)
	if err != nil {
		klog.V(log.DEBUG).Infof("Error listing iptables chains in %s table: %s", table, err)
		return false, err
	}

	for _, val := range existingChains {
		if val == chain {
			return true, nil
		}
	}

	return false, nil
}

func (i *Controller) updateIngressRulesForHealthCheck(resourceName, cniIfaceIP, globalIP string, addRules bool) error {
	ruleSpec := []string{"-p", "icmp", "-d", globalIP, "-j", "DNAT", "--to", cniIfaceIP}

	if addRules {
		klog.V(log.DEBUG).Infof("Installing iptable ingress rules for %s: %s", resourceName, strings.Join(ruleSpec, " "))

		if err := i.ipt.AppendUnique("nat", constants.SmGlobalnetIngressChain, ruleSpec...); err != nil {
			return fmt.Errorf("error appending iptables rule \"%s\": %v", strings.Join(ruleSpec, " "), err)
		}
	} else {
		klog.V(log.DEBUG).Infof("Deleting iptable ingress rules for %s : %s", resourceName, strings.Join(ruleSpec, " "))
		if err := i.ipt.Delete("nat", constants.SmGlobalnetIngressChain, ruleSpec...); err != nil {
			return fmt.Errorf("error deleting iptables rule \"%s\": %v", strings.Join(ruleSpec, " "), err)
		}
	}

	return nil
}
