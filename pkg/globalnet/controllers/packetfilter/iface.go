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

package packetfilter

import (
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type Interface interface {
	AddClusterEgressRules(sourceIP, snatIP, globalNetIPTableMark string) error
	RemoveClusterEgressRules(sourceIP, snatIP, globalNetIPTableMark string) error
	AddIngressRulesForHeadlessSvc(globalIP, podIP string, targetType TargetType) error
	RemoveIngressRulesForHeadlessSvc(globalIP, podIP string, targetType TargetType) error
	GetKubeProxyClusterIPServiceChainName(service *corev1.Service, kubeProxyServiceChainPrefix string) (string, bool, error)
	AddIngressRulesForHealthCheck(cniIfaceIP, globalIP string) error
	RemoveIngressRulesForHealthCheck(cniIfaceIP, globalIP string) error
	AddEgressRulesForHeadlessSvc(key, sourceIP, snatIP, globalNetIPTableMark string, targetType TargetType) error
	RemoveEgressRulesForHeadlessSvc(key, sourceIP, snatIP, globalNetIPTableMark string, targetType TargetType) error
	AddEgressRulesForPods(namespace, ipSetName, snatIP, globalNetIPTableMark string) error
	RemoveEgressRulesForPods(namespace, ipSetName, snatIP, globalNetIPTableMark string) error
	AddEgressRulesForNamespace(namespace, ipSetName, snatIP, globalNetIPTableMark string) error
	RemoveEgressRulesForNamespace(namespace, ipSetName, snatIP, globalNetIPTableMark string) error
	FlushIPTableChain(table, chainName string) error
	DeleteIPTableChain(table, chainName string) error
	DeleteIPTableRule(table, chainName, jumpTarget string) error
}

type packetFilter struct {
	pFilter packetfilter.Interface
}

type TargetType string

const (
	PodTarget       TargetType = "Pod"
	EndpointsTarget TargetType = "Endpoints"
)

var logger = log.Logger{Logger: logf.Log.WithName("IPTables")}

func New() (Interface, error) {
	pFilter, err := packetfilter.New()
	if err != nil {
		return nil, err //nolint:wrapcheck  // Let the caller wrap it
	}

	return &packetFilter{
		pFilter: pFilter,
	}, nil
}

func (i *packetFilter) AddClusterEgressRules(subnet, snatIP, globalNetIPTableMark string) error {
	ruleSpec := []string{"-p", "all", "-s", subnet, "-m", "mark", "--mark", globalNetIPTableMark, "-j", "SNAT", "--to", snatIP}
	logger.V(log.DEBUG).Infof("Installing iptable egress rules for Cluster: %s", strings.Join(ruleSpec, " "))

	if err := i.pFilter.AppendUnique("nat", constants.SmGlobalnetEgressChainForCluster, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error appending packetfilter rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *packetFilter) RemoveClusterEgressRules(subnet, snatIP, globalNetIPTableMark string) error {
	ruleSpec := []string{"-p", "all", "-s", subnet, "-m", "mark", "--mark", globalNetIPTableMark, "-j", "SNAT", "--to", snatIP}
	logger.V(log.DEBUG).Infof("Deleting iptable egress rules for Cluster: %s", strings.Join(ruleSpec, " "))

	if err := i.pFilter.Delete("nat", constants.SmGlobalnetEgressChainForCluster, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error deleting packetfilter rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *packetFilter) AddIngressRulesForHeadlessSvc(globalIP, ip string, targetType TargetType) error {
	if globalIP == "" || ip == "" {
		return fmt.Errorf("globalIP %q or %s IP %q cannot be empty", globalIP, targetType, ip)
	}

	ruleSpec := []string{"-d", globalIP, "-j", "DNAT", "--to", ip}
	logger.V(log.DEBUG).Infof("Installing packetfilter rule for Headless SVC %s for %s", strings.Join(ruleSpec, " "), targetType)

	if err := i.pFilter.AppendUnique("nat", constants.SmGlobalnetIngressChain, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error appending packetfilter rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *packetFilter) RemoveIngressRulesForHeadlessSvc(globalIP, ip string, targetType TargetType) error {
	if globalIP == "" || ip == "" {
		return fmt.Errorf("globalIP %q or %s IP %q cannot be empty", globalIP, targetType, ip)
	}

	ruleSpec := []string{"-d", globalIP, "-j", "DNAT", "--to", ip}

	logger.V(log.DEBUG).Infof("Deleting packetfilter rule for Headless SVC %s for %s", strings.Join(ruleSpec, " "), targetType)

	if err := i.pFilter.Delete("nat", constants.SmGlobalnetIngressChain, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error deleting packetfilter rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *packetFilter) GetKubeProxyClusterIPServiceChainName(service *corev1.Service,
	kubeProxyServiceChainPrefix string,
) (string, bool, error) {
	// CNIs that use kube-proxy with packetfilter for loadbalancing create an packetfilter chain for each service
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

		chainExists, err := i.pFilter.ChainExists("nat", chainName)
		if err != nil {
			return "", false, errors.Wrapf(err, "error checking if chain %s exists", chainName)
		}

		if chainExists {
			return chainName, true, nil
		}
	}

	return "", false, nil
}

func (i *packetFilter) AddIngressRulesForHealthCheck(cniIfaceIP, globalIP string) error {
	ruleSpec := []string{"-p", "icmp", "-d", globalIP, "-j", "DNAT", "--to", cniIfaceIP}
	logger.V(log.DEBUG).Infof("Installing iptable ingress rules for Node: %s", strings.Join(ruleSpec, " "))

	if err := i.pFilter.AppendUnique("nat", constants.SmGlobalnetIngressChain, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error appending packetfilter rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *packetFilter) RemoveIngressRulesForHealthCheck(cniIfaceIP, globalIP string) error {
	ruleSpec := []string{"-p", "icmp", "-d", globalIP, "-j", "DNAT", "--to", cniIfaceIP}
	logger.V(log.DEBUG).Infof("Deleting iptable ingress rules for Node: %s", strings.Join(ruleSpec, " "))

	if err := i.pFilter.Delete("nat", constants.SmGlobalnetIngressChain, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error deleting packetfilter rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *packetFilter) AddEgressRulesForHeadlessSvc(key, sourceIP, snatIP, globalNetIPTableMark string, targetType TargetType) error {
	ruleSpec := []string{"-p", "all", "-s", sourceIP, "-m", "mark", "--mark", globalNetIPTableMark, "-j", "SNAT", "--to", snatIP}
	logger.V(log.DEBUG).Infof("Installing iptable egress rules for HDLS SVC %q for %s: %s", key, targetType, strings.Join(ruleSpec, " "))

	var chain string

	if targetType == PodTarget {
		chain = constants.SmGlobalnetEgressChainForHeadlessSvcPods
	} else if targetType == EndpointsTarget {
		chain = constants.SmGlobalnetEgressChainForHeadlessSvcEPs
	}

	if err := i.pFilter.AppendUnique("nat", chain, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error appending packetfilter rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *packetFilter) RemoveEgressRulesForHeadlessSvc(key, sourceIP, snatIP, globalNetIPTableMark string, targetType TargetType) error {
	ruleSpec := []string{"-p", "all", "-s", sourceIP, "-m", "mark", "--mark", globalNetIPTableMark, "-j", "SNAT", "--to", snatIP}
	logger.V(log.DEBUG).Infof("Deleting iptable egress rules for HDLS SVC %q for %s: %s", key, targetType, strings.Join(ruleSpec, " "))

	var chain string

	if targetType == PodTarget {
		chain = constants.SmGlobalnetEgressChainForHeadlessSvcPods
	} else if targetType == EndpointsTarget {
		chain = constants.SmGlobalnetEgressChainForHeadlessSvcEPs
	}

	if err := i.pFilter.Delete("nat", chain, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error deleting packetfilter rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *packetFilter) AddEgressRulesForPods(key, ipSetName, snatIP, globalNetIPTableMark string) error {
	ruleSpec := []string{
		"-p", "all", "-m", "set", "--match-set", ipSetName, "src", "-m", "mark",
		"--mark", globalNetIPTableMark, "-j", "SNAT", "--to", snatIP,
	}
	logger.V(log.DEBUG).Infof("Installing iptable egress rules for Pods %q: %s", key, strings.Join(ruleSpec, " "))

	if err := i.pFilter.AppendUnique("nat", constants.SmGlobalnetEgressChainForPods, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error appending packetfilter rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *packetFilter) RemoveEgressRulesForPods(key, ipSetName, snatIP, globalNetIPTableMark string) error {
	ruleSpec := []string{
		"-p", "all", "-m", "set", "--match-set", ipSetName, "src", "-m", "mark",
		"--mark", globalNetIPTableMark, "-j", "SNAT", "--to", snatIP,
	}
	logger.V(log.DEBUG).Infof("Deleting iptable egress rules for Pods %q: %s", key, strings.Join(ruleSpec, " "))

	if err := i.pFilter.Delete("nat", constants.SmGlobalnetEgressChainForPods, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error deleting packetfilter rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *packetFilter) AddEgressRulesForNamespace(namespace, ipSetName, snatIP, globalNetIPTableMark string) error {
	ruleSpec := []string{
		"-p", "all", "-m", "set", "--match-set", ipSetName, "src", "-m", "mark",
		"--mark", globalNetIPTableMark, "-j", "SNAT", "--to", snatIP,
	}
	logger.V(log.DEBUG).Infof("Installing iptable egress rules for Namespace %q: %s", namespace, strings.Join(ruleSpec, " "))

	if err := i.pFilter.AppendUnique("nat", constants.SmGlobalnetEgressChainForNamespace, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error appending packetfilter rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *packetFilter) RemoveEgressRulesForNamespace(namespace, ipSetName, snatIP, globalNetIPTableMark string) error {
	ruleSpec := []string{
		"-p", "all", "-m", "set", "--match-set", ipSetName, "src", "-m", "mark",
		"--mark", globalNetIPTableMark, "-j", "SNAT", "--to", snatIP,
	}
	logger.V(log.DEBUG).Infof("Deleting iptable egress rules for Namespace %q: %s", namespace, strings.Join(ruleSpec, " "))

	if err := i.pFilter.Delete("nat", constants.SmGlobalnetEgressChainForNamespace, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error deleting packetfilter rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (i *packetFilter) FlushIPTableChain(table, chainName string) error {
	logger.Infof("Flushing iptable rules in %q chain of table %q", chainName, table)

	if err := i.pFilter.ClearChain(table, chainName); err != nil {
		return errors.Wrapf(err, "error flushing packetfilter rules in %q chain of table %q", chainName, table)
	}

	return nil
}

func (i *packetFilter) DeleteIPTableChain(table, chainName string) error {
	logger.Infof("Deleting iptable chain %q of table %q", chainName, table)

	if err := i.pFilter.DeleteChain(table, chainName); err != nil {
		return errors.Wrapf(err, "error deleting iptable chain %q of table %q", chainName, table)
	}

	return nil
}

func (i *packetFilter) DeleteIPTableRule(table, chainName, jumpTarget string) error {
	ruleSpec := []string{"-j", jumpTarget}
	if err := i.pFilter.Delete(table, chainName, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error deleting packetfilter rule \"%s\"", strings.Join(ruleSpec, " "))
	}

	return nil
}
