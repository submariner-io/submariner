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

package iptables

import (
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/iptables"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog"
)

type Interface interface {
	AddClusterEgressRules(sourceIP, snatIP, globalNetIPTableMark string) error
	RemoveClusterEgressRules(sourceIP, snatIP, globalNetIPTableMark string) error
	AddIngressRulesForService(globalIP, chainName string) error
	RemoveIngressRulesForService(globalIP, chainName string) error
	AddIngressRulesForHeadlessSvcPod(globalIP, podIP string) error
	RemoveIngressRulesForHeadlessSvcPod(globalIP, podIP string) error
	GetKubeProxyClusterIPServiceChainName(service *corev1.Service, kubeProxyServiceChainPrefix string) (string, bool, error)
	AddIngressRulesForHealthCheck(cniIfaceIP, globalIP string) error
	RemoveIngressRulesForHealthCheck(cniIfaceIP, globalIP string) error
	AddEgressRulesForHeadlessSVCPods(key, sourceIP, snatIP, globalNetIPTableMark string) error
	RemoveEgressRulesForHeadlessSVCPods(key, sourceIP, snatIP, globalNetIPTableMark string) error
	AddEgressRulesForPods(namespace, ipSetName, snatIP, globalNetIPTableMark string) error
	RemoveEgressRulesForPods(namespace, ipSetName, snatIP, globalNetIPTableMark string) error
	AddEgressRulesForNamespace(namespace, ipSetName, snatIP, globalNetIPTableMark string) error
	RemoveEgressRulesForNamespace(namespace, ipSetName, snatIP, globalNetIPTableMark string) error
}

type ipTables struct {
	ipt iptables.Interface
}

func New() (Interface, error) {
	iptableHandler, err := iptables.New()
	if err != nil {
		return nil, err
	}

	iptableIface := &ipTables{
		ipt: iptableHandler,
	}

	return iptableIface, nil
}

func (i *ipTables) AddClusterEgressRules(subnet, snatIP, globalNetIPTableMark string) error {
	ruleSpec := []string{"-p", "all", "-s", subnet, "-m", "mark", "--mark", globalNetIPTableMark, "-j", "SNAT", "--to", snatIP}
	klog.V(log.DEBUG).Infof("Installing iptable egress rules for Cluster: %s", strings.Join(ruleSpec, " "))

	if err := i.ipt.AppendUnique("nat", constants.SmGlobalnetEgressChainForCluster, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error appending iptables rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *ipTables) RemoveClusterEgressRules(subnet, snatIP, globalNetIPTableMark string) error {
	ruleSpec := []string{"-p", "all", "-s", subnet, "-m", "mark", "--mark", globalNetIPTableMark, "-j", "SNAT", "--to", snatIP}
	klog.V(log.DEBUG).Infof("Deleting iptable egress rules for Cluster: %s", strings.Join(ruleSpec, " "))

	if err := i.ipt.Delete("nat", constants.SmGlobalnetEgressChainForCluster, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error deleting iptables rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *ipTables) ipTableChainExists(table, chain string) (bool, error) {
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

func (i *ipTables) AddIngressRulesForService(globalIP, chainName string) error {
	if globalIP == "" || chainName == "" {
		return fmt.Errorf("globalIP %q or chainName %q cannot be empty", globalIP, chainName)
	}

	ruleSpec := []string{"-d", globalIP, "-j", chainName}
	klog.V(log.DEBUG).Infof("Installing iptables rule for Service %s", strings.Join(ruleSpec, " "))

	if err := i.ipt.AppendUnique("nat", constants.SmGlobalnetIngressChain, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error appending iptables rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *ipTables) RemoveIngressRulesForService(globalIP, chainName string) error {
	if globalIP == "" || chainName == "" {
		return fmt.Errorf("globalIP %q or chainName %q cannot be empty", globalIP, chainName)
	}

	ruleSpec := []string{"-d", globalIP, "-j", chainName}

	klog.V(log.DEBUG).Infof("Deleting iptable ingress rule for Service: %s", strings.Join(ruleSpec, " "))

	if err := i.ipt.Delete("nat", constants.SmGlobalnetIngressChain, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error deleting iptables rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *ipTables) AddIngressRulesForHeadlessSvcPod(globalIP, podIP string) error {
	if globalIP == "" || podIP == "" {
		return fmt.Errorf("globalIP %q or podIP %q cannot be empty", globalIP, podIP)
	}

	ruleSpec := []string{"-d", globalIP, "-j", "DNAT", "--to", podIP}
	klog.V(log.DEBUG).Infof("Installing iptables rule for Headless SVC Pod %s", strings.Join(ruleSpec, " "))

	if err := i.ipt.AppendUnique("nat", constants.SmGlobalnetIngressChain, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error appending iptables rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *ipTables) RemoveIngressRulesForHeadlessSvcPod(globalIP, podIP string) error {
	if globalIP == "" || podIP == "" {
		return fmt.Errorf("globalIP %q or podIP %q cannot be empty", globalIP, podIP)
	}

	ruleSpec := []string{"-d", globalIP, "-j", "DNAT", "--to", podIP}

	klog.V(log.DEBUG).Infof("Deleting iptables rule for Headless SVC Pod %s", strings.Join(ruleSpec, " "))

	if err := i.ipt.Delete("nat", constants.SmGlobalnetIngressChain, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error deleting iptables rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *ipTables) GetKubeProxyClusterIPServiceChainName(service *corev1.Service,
	kubeProxyServiceChainPrefix string) (string, bool, error) {
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

		chainExists, err := i.ipTableChainExists("nat", chainName)
		if err != nil {
			return "", false, err
		}

		if chainExists {
			return chainName, true, nil
		}
	}

	return "", false, nil
}

func (i *ipTables) AddIngressRulesForHealthCheck(cniIfaceIP, globalIP string) error {
	ruleSpec := []string{"-p", "icmp", "-d", globalIP, "-j", "DNAT", "--to", cniIfaceIP}
	klog.V(log.DEBUG).Infof("Installing iptable ingress rules for Node: %s", strings.Join(ruleSpec, " "))

	if err := i.ipt.AppendUnique("nat", constants.SmGlobalnetIngressChain, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error appending iptables rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *ipTables) RemoveIngressRulesForHealthCheck(cniIfaceIP, globalIP string) error {
	ruleSpec := []string{"-p", "icmp", "-d", globalIP, "-j", "DNAT", "--to", cniIfaceIP}
	klog.V(log.DEBUG).Infof("Deleting iptable ingress rules for Node: %s", strings.Join(ruleSpec, " "))

	if err := i.ipt.Delete("nat", constants.SmGlobalnetIngressChain, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error deleting iptables rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *ipTables) AddEgressRulesForHeadlessSVCPods(key, sourceIP, snatIP, globalNetIPTableMark string) error {
	ruleSpec := []string{"-p", "all", "-s", sourceIP, "-m", "mark", "--mark", globalNetIPTableMark, "-j", "SNAT", "--to", snatIP}
	klog.V(log.DEBUG).Infof("Installing iptable egress rules for HDLS SVC Pod %q: %s", key, strings.Join(ruleSpec, " "))

	if err := i.ipt.AppendUnique("nat", constants.SmGlobalnetEgressChainForHeadlessSvcPods, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error appending iptables rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *ipTables) RemoveEgressRulesForHeadlessSVCPods(key, sourceIP, snatIP, globalNetIPTableMark string) error {
	ruleSpec := []string{"-p", "all", "-s", sourceIP, "-m", "mark", "--mark", globalNetIPTableMark, "-j", "SNAT", "--to", snatIP}
	klog.V(log.DEBUG).Infof("Deleting iptable egress rules for HDLS SVC Pod %q: %s", key, strings.Join(ruleSpec, " "))

	if err := i.ipt.Delete("nat", constants.SmGlobalnetEgressChainForHeadlessSvcPods, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error deleting iptables rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *ipTables) AddEgressRulesForPods(key, ipSetName, snatIP, globalNetIPTableMark string) error {
	ruleSpec := []string{
		"-p", "all", "-m", "set", "--match-set", ipSetName, "src", "-m", "mark",
		"--mark", globalNetIPTableMark, "-j", "SNAT", "--to", snatIP,
	}
	klog.V(log.DEBUG).Infof("Installing iptable egress rules for Pods %q: %s", key, strings.Join(ruleSpec, " "))

	if err := i.ipt.AppendUnique("nat", constants.SmGlobalnetEgressChainForPods, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error appending iptables rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *ipTables) RemoveEgressRulesForPods(key, ipSetName, snatIP, globalNetIPTableMark string) error {
	ruleSpec := []string{
		"-p", "all", "-m", "set", "--match-set", ipSetName, "src", "-m", "mark",
		"--mark", globalNetIPTableMark, "-j", "SNAT", "--to", snatIP,
	}
	klog.V(log.DEBUG).Infof("Deleting iptable egress rules for Pods %q: %s", key, strings.Join(ruleSpec, " "))

	if err := i.ipt.Delete("nat", constants.SmGlobalnetEgressChainForPods, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error deleting iptables rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *ipTables) AddEgressRulesForNamespace(namespace, ipSetName, snatIP, globalNetIPTableMark string) error {
	ruleSpec := []string{
		"-p", "all", "-m", "set", "--match-set", ipSetName, "src", "-m", "mark",
		"--mark", globalNetIPTableMark, "-j", "SNAT", "--to", snatIP,
	}
	klog.V(log.DEBUG).Infof("Installing iptable egress rules for Namespace %q: %s", namespace, strings.Join(ruleSpec, " "))

	if err := i.ipt.AppendUnique("nat", constants.SmGlobalnetEgressChainForNamespace, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error appending iptables rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *ipTables) RemoveEgressRulesForNamespace(namespace, ipSetName, snatIP, globalNetIPTableMark string) error {
	ruleSpec := []string{
		"-p", "all", "-m", "set", "--match-set", ipSetName, "src", "-m", "mark",
		"--mark", globalNetIPTableMark, "-j", "SNAT", "--to", snatIP,
	}
	klog.V(log.DEBUG).Infof("Deleting iptable egress rules for Namespace %q: %s", namespace, strings.Join(ruleSpec, " "))

	if err := i.ipt.Delete("nat", constants.SmGlobalnetEgressChainForNamespace, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error deleting iptables rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}
