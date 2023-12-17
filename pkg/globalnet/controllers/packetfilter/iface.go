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
	"fmt"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type Interface interface {
	AddClusterEgressRules(sourceIP, snatIP, globalNetIPTableMark string) error
	RemoveClusterEgressRules(sourceIP, snatIP, globalNetIPTableMark string) error
	AddIngressRulesForHeadlessSvc(globalIP, podIP string, targetType TargetType) error
	RemoveIngressRulesForHeadlessSvc(globalIP, podIP string, targetType TargetType) error
	AddIngressRulesForHealthCheck(cniIfaceIP, globalIP string) error
	RemoveIngressRulesForHealthCheck(cniIfaceIP, globalIP string) error
	AddEgressRulesForHeadlessSvc(key, sourceIP, snatIP, globalNetIPTableMark string, targetType TargetType) error
	RemoveEgressRulesForHeadlessSvc(key, sourceIP, snatIP, globalNetIPTableMark string, targetType TargetType) error

	AddEgressRulesForPods(key, ipSetName, snatIP, globalNetIPTableMark string) error
	RemoveEgressRulesForPods(key, ipSetName, snatIP, globalNetIPTableMark string) error
	AddEgressRulesForNamespace(namespace, ipSetName, snatIP, globalNetIPTableMark string) error
	RemoveEgressRulesForNamespace(namespace, ipSetName, snatIP, globalNetIPTableMark string) error
	FlushNatChain(chainName string) error
	DeleteNatChain(chainName string) error
}

type pfilter struct {
	pFilter packetfilter.Interface
}

type TargetType string

const (
	PodTarget       TargetType = "Pod"
	EndpointsTarget TargetType = "Endpoints"
)

var logger = log.Logger{Logger: logf.Log.WithName("PacketFilter")}

func New() (Interface, error) {
	pFilterHandler, err := packetfilter.New()
	if err != nil {
		return nil, err //nolint:wrapcheck  // Let the caller wrap it
	}

	pFilterIface := &pfilter{
		pFilter: pFilterHandler,
	}

	return pFilterIface, nil
}

func (i *pfilter) AddClusterEgressRules(subnet, snatIP, globalNetIPTableMark string) error {
	ruleSpec := packetfilter.Rule{
		Proto:     packetfilter.RuleProtoAll,
		SrcCIDR:   subnet,
		MarkValue: globalNetIPTableMark,
		SnatCIDR:  snatIP,
		Action:    packetfilter.RuleActionSNAT,
	}

	if err := i.pFilter.AppendUnique(packetfilter.TableTypeNAT, constants.SmGlobalnetEgressChainForCluster, &ruleSpec); err != nil {
		return errors.Wrapf(err, "unable to append rule %+v", &ruleSpec)
	}

	return nil
}

func (i *pfilter) RemoveClusterEgressRules(subnet, snatIP, globalNetIPTableMark string) error {
	ruleSpec := packetfilter.Rule{
		Proto:     packetfilter.RuleProtoAll,
		SrcCIDR:   subnet,
		MarkValue: globalNetIPTableMark,
		SnatCIDR:  snatIP,
		Action:    packetfilter.RuleActionSNAT,
	}

	if err := i.pFilter.Delete(packetfilter.TableTypeNAT, constants.SmGlobalnetEgressChainForCluster, &ruleSpec); err != nil {
		return errors.Wrapf(err, "error deleting packetfilter rule %+v", &ruleSpec)
	}

	return nil
}

func (i *pfilter) AddIngressRulesForHeadlessSvc(globalIP, ip string, targetType TargetType) error {
	if globalIP == "" || ip == "" {
		return fmt.Errorf("globalIP %q or %s IP %q cannot be empty", globalIP, targetType, ip)
	}

	ruleSpec := packetfilter.Rule{
		DestCIDR: globalIP,
		DnatCIDR: ip,
		Action:   packetfilter.RuleActionDNAT,
	}
	logger.V(log.DEBUG).Infof("Installing packetfilter rule for Headless SVC %+v for %s", &ruleSpec, targetType)

	if err := i.pFilter.AppendUnique(packetfilter.TableTypeNAT, constants.SmGlobalnetIngressChain, &ruleSpec); err != nil {
		return errors.Wrapf(err, "unable to append rule %+v", &ruleSpec)
	}

	return nil
}

func (i *pfilter) RemoveIngressRulesForHeadlessSvc(globalIP, ip string, targetType TargetType) error {
	if globalIP == "" || ip == "" {
		return fmt.Errorf("globalIP %q or %s IP %q cannot be empty", globalIP, targetType, ip)
	}

	ruleSpec := packetfilter.Rule{
		DestCIDR: globalIP,
		DnatCIDR: ip,
		Action:   packetfilter.RuleActionDNAT,
	}
	logger.V(log.DEBUG).Infof("Deleting iptables rule for Headless SVC %+v for %s", &ruleSpec, targetType)

	if err := i.pFilter.Delete(packetfilter.TableTypeNAT, constants.SmGlobalnetIngressChain, &ruleSpec); err != nil {
		return errors.Wrapf(err, "error deleting packetfilter rule %+v", &ruleSpec)
	}

	return nil
}

func (i *pfilter) AddIngressRulesForHealthCheck(cniIfaceIP, globalIP string) error {
	ruleSpec := packetfilter.Rule{
		Proto:    packetfilter.RuleProtoICMP,
		DestCIDR: globalIP,
		DnatCIDR: cniIfaceIP,
		Action:   packetfilter.RuleActionDNAT,
	}
	logger.V(log.DEBUG).Infof("Installing packetfilter ingress rules for Node: %+v", &ruleSpec)

	if err := i.pFilter.AppendUnique(packetfilter.TableTypeNAT, constants.SmGlobalnetIngressChain, &ruleSpec); err != nil {
		return errors.Wrapf(err, "unable to append rule %+v", &ruleSpec)
	}

	return nil
}

func (i *pfilter) RemoveIngressRulesForHealthCheck(cniIfaceIP, globalIP string) error {
	ruleSpec := packetfilter.Rule{
		Proto:    packetfilter.RuleProtoICMP,
		DestCIDR: globalIP,
		DnatCIDR: cniIfaceIP,
		Action:   packetfilter.RuleActionDNAT,
	}
	logger.V(log.DEBUG).Infof("Deleting packetfilter ingress rules for Node: %+v", &ruleSpec)

	if err := i.pFilter.Delete(packetfilter.TableTypeNAT, constants.SmGlobalnetIngressChain, &ruleSpec); err != nil {
		return errors.Wrapf(err, "error deleting packetfilter rule %+v", &ruleSpec)
	}

	return nil
}

func (i *pfilter) AddEgressRulesForHeadlessSvc(key, sourceIP, snatIP, globalNetIPTableMark string, targetType TargetType) error {
	ruleSpec := packetfilter.Rule{
		Proto:     packetfilter.RuleProtoAll,
		SrcCIDR:   sourceIP,
		MarkValue: globalNetIPTableMark,
		SnatCIDR:  snatIP,
		Action:    packetfilter.RuleActionSNAT,
	}
	logger.V(log.DEBUG).Infof("Installing packetfilter egress rules for HDLS SVC %q for %s: %+v", key, targetType, &ruleSpec)

	var chain string

	if targetType == PodTarget {
		chain = constants.SmGlobalnetEgressChainForHeadlessSvcPods
	} else if targetType == EndpointsTarget {
		chain = constants.SmGlobalnetEgressChainForHeadlessSvcEPs
	}

	if err := i.pFilter.AppendUnique(packetfilter.TableTypeNAT, chain, &ruleSpec); err != nil {
		return errors.Wrapf(err, "error appending packetfilter rule %+v", &ruleSpec)
	}

	return nil
}

func (i *pfilter) RemoveEgressRulesForHeadlessSvc(key, sourceIP, snatIP, globalNetIPTableMark string, targetType TargetType) error {
	ruleSpec := packetfilter.Rule{
		Proto:     packetfilter.RuleProtoAll,
		SrcCIDR:   sourceIP,
		MarkValue: globalNetIPTableMark,
		SnatCIDR:  snatIP,
		Action:    packetfilter.RuleActionSNAT,
	}
	logger.V(log.DEBUG).Infof("Deleting iptable egress rules for HDLS SVC %q for %s: %+v", key, targetType, &ruleSpec)

	var chain string

	if targetType == PodTarget {
		chain = constants.SmGlobalnetEgressChainForHeadlessSvcPods
	} else if targetType == EndpointsTarget {
		chain = constants.SmGlobalnetEgressChainForHeadlessSvcEPs
	}

	if err := i.pFilter.Delete(packetfilter.TableTypeNAT, chain, &ruleSpec); err != nil {
		return errors.Wrapf(err, "error deleting packetfilter rule %+v", &ruleSpec)
	}

	return nil
}

func (i *pfilter) AddEgressRulesForPods(key, ipSetName, snatIP, globalNetIPTableMark string) error {
	ruleSpec := &packetfilter.Rule{
		Proto:      packetfilter.RuleProtoAll,
		SrcSetName: ipSetName,
		SnatCIDR:   snatIP,
		MarkValue:  globalNetIPTableMark,
		Action:     packetfilter.RuleActionSNAT,
	}

	logger.V(log.DEBUG).Infof("Installing egress rules for pods %q: %q", key, ruleSpec)

	if err := i.pFilter.AppendUnique(packetfilter.TableTypeNAT, constants.SmGlobalnetEgressChainForPods, ruleSpec); err != nil {
		return errors.Wrapf(err, "error appending rule \"%q\" chain:%s", ruleSpec, constants.SmGlobalnetEgressChainForPods)
	}

	return nil
}

func (i *pfilter) RemoveEgressRulesForPods(key, ipSetName, snatIP, globalNetIPTableMark string) error {
	ruleSpec := &packetfilter.Rule{
		Proto:      packetfilter.RuleProtoAll,
		SrcSetName: ipSetName,
		SnatCIDR:   snatIP,
		MarkValue:  globalNetIPTableMark,
		Action:     packetfilter.RuleActionSNAT,
	}

	logger.V(log.DEBUG).Infof("Deleting egress rules for Pods %q: %q", key, ruleSpec)

	if err := i.pFilter.Delete(packetfilter.TableTypeNAT, constants.SmGlobalnetEgressChainForPods, ruleSpec); err != nil {
		return errors.Wrapf(err, "error  rule \"%q\" chain:%s", ruleSpec, constants.SmGlobalnetEgressChainForPods)
	}

	return nil
}

func (i *pfilter) AddEgressRulesForNamespace(namespace, ipSetName, snatIP, globalNetIPTableMark string) error {
	ruleSpec := &packetfilter.Rule{
		Proto:      packetfilter.RuleProtoAll,
		SrcSetName: ipSetName,
		SnatCIDR:   snatIP,
		MarkValue:  globalNetIPTableMark,
		Action:     packetfilter.RuleActionSNAT,
	}

	logger.V(log.DEBUG).Infof("Installing egress rules for Namespace %q: %q", namespace, ruleSpec)

	if err := i.pFilter.AppendUnique(packetfilter.TableTypeNAT, constants.SmGlobalnetEgressChainForNamespace, ruleSpec); err != nil {
		return errors.Wrapf(err, "error appending rule \"%q\" chain:%s", ruleSpec, constants.SmGlobalnetEgressChainForNamespace)
	}

	return nil
}

func (i *pfilter) RemoveEgressRulesForNamespace(namespace, ipSetName, snatIP, globalNetIPTableMark string) error {
	ruleSpec := &packetfilter.Rule{
		Proto:      packetfilter.RuleProtoAll,
		SrcSetName: ipSetName,
		SnatCIDR:   snatIP,
		MarkValue:  globalNetIPTableMark,
		Action:     packetfilter.RuleActionSNAT,
	}

	logger.V(log.DEBUG).Infof("Deleting egress rules for Namespace %q: %q", namespace, ruleSpec)

	if err := i.pFilter.Delete(packetfilter.TableTypeNAT, constants.SmGlobalnetEgressChainForNamespace, ruleSpec); err != nil {
		return errors.Wrapf(err, "error  rule \"%q\" chain:%s", ruleSpec, constants.SmGlobalnetEgressChainForNamespace)
	}

	return nil
}

func (i *pfilter) FlushNatChain(chainName string) error {
	logger.Infof("Flushing packetfilter rules in %q chain of table NAT", chainName)

	if err := i.pFilter.ClearChain(packetfilter.TableTypeNAT, chainName); err != nil {
		return errors.Wrapf(err, "error flushing packetfilter rules in %q chain of table NAT", chainName)
	}

	return nil
}

func (i *pfilter) DeleteNatChain(chainName string) error {
	logger.Infof("Deleting packetfilter chain %q of table NAT", chainName)

	if err := i.pFilter.DeleteChain(packetfilter.TableTypeNAT, chainName); err != nil {
		return errors.Wrapf(err, "error deleting packetfilter chain %q of table NAT", chainName)
	}

	return nil
}
