/*
© 2021 Red Hat, Inc. and others

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
	"fmt"
	"strings"

	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/iptables"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/util"
)

func (i *Controller) initIPTableChains() error {
	klog.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetIngressChain)

	if err := util.CreateChainIfNotExists(i.ipt, "nat", constants.SmGlobalnetIngressChain); err != nil {
		return fmt.Errorf("error creating iptables chain %s: %v", constants.SmGlobalnetIngressChain, err)
	}

	forwardToSubGlobalNetChain := []string{"-j", constants.SmGlobalnetIngressChain}
	if err := util.PrependUnique(i.ipt, "nat", "PREROUTING", forwardToSubGlobalNetChain); err != nil {
		klog.Errorf("error appending iptables rule %q: %v\n", strings.Join(forwardToSubGlobalNetChain, " "), err)
	}

	klog.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetEgressChain)

	if err := util.CreateChainIfNotExists(i.ipt, "nat", constants.SmGlobalnetEgressChain); err != nil {
		return fmt.Errorf("error creating iptables chain %s: %v", constants.SmGlobalnetEgressChain, err)
	}

	klog.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmPostRoutingChain)

	if err := util.CreateChainIfNotExists(i.ipt, "nat", constants.SmPostRoutingChain); err != nil {
		return fmt.Errorf("error creating iptables chain %s: %v", constants.SmPostRoutingChain, err)
	}

	forwardToSubGlobalNetChain = []string{"-j", constants.SmGlobalnetEgressChain}
	if err := util.PrependUnique(i.ipt, "nat", constants.SmPostRoutingChain, forwardToSubGlobalNetChain); err != nil {
		klog.Errorf("error inserting iptables rule %q: %v\n", strings.Join(forwardToSubGlobalNetChain, " "), err)
	}

	if err := CreateGlobalNetMarkingChain(i.ipt); err != nil {
		return err
	}

	forwardToSubGlobalNetChain = []string{"-j", constants.SmGlobalnetMarkChain}
	if err := util.PrependUnique(i.ipt, "nat", constants.SmGlobalnetEgressChain, forwardToSubGlobalNetChain); err != nil {
		klog.Errorf("error inserting iptables rule %q: %v\n", strings.Join(forwardToSubGlobalNetChain, " "), err)
	}

	klog.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetEgressChainForPods)

	if err := util.CreateChainIfNotExists(i.ipt, "nat", constants.SmGlobalnetEgressChainForPods); err != nil {
		return fmt.Errorf("error creating iptables chain %s: %v", constants.SmGlobalnetEgressChainForPods, err)
	}

	klog.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetEgressChainForHeadlessSvcPods)

	if err := util.CreateChainIfNotExists(i.ipt, "nat", constants.SmGlobalnetEgressChainForHeadlessSvcPods); err != nil {
		return fmt.Errorf("error creating iptables chain %s: %v", constants.SmGlobalnetEgressChainForHeadlessSvcPods, err)
	}

	klog.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetEgressChainForNamespace)

	if err := util.CreateChainIfNotExists(i.ipt, "nat", constants.SmGlobalnetEgressChainForNamespace); err != nil {
		return fmt.Errorf("error creating iptables chain %s: %v", constants.SmGlobalnetEgressChainForNamespace, err)
	}

	klog.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetEgressChainForCluster)

	if err := util.CreateChainIfNotExists(i.ipt, "nat", constants.SmGlobalnetEgressChainForCluster); err != nil {
		return fmt.Errorf("error creating iptables chain %s: %v", constants.SmGlobalnetEgressChainForCluster, err)
	}

	forwardToSubGlobalNetChain = []string{"-j", constants.SmGlobalnetEgressChainForPods}
	if err := util.InsertUnique(i.ipt, "nat", constants.SmGlobalnetEgressChain, 2, forwardToSubGlobalNetChain); err != nil {
		klog.Errorf("error inserting iptables rule %q: %v\n", strings.Join(forwardToSubGlobalNetChain, " "), err)
	}

	forwardToSubGlobalNetChain = []string{"-j", constants.SmGlobalnetEgressChainForHeadlessSvcPods}
	if err := util.InsertUnique(i.ipt, "nat", constants.SmGlobalnetEgressChain, 3, forwardToSubGlobalNetChain); err != nil {
		klog.Errorf("error inserting iptables rule %q: %v\n", strings.Join(forwardToSubGlobalNetChain, " "), err)
	}

	forwardToSubGlobalNetChain = []string{"-j", constants.SmGlobalnetEgressChainForNamespace}
	if err := util.InsertUnique(i.ipt, "nat", constants.SmGlobalnetEgressChain, 4, forwardToSubGlobalNetChain); err != nil {
		klog.Errorf("error inserting iptables rule %q: %v\n", strings.Join(forwardToSubGlobalNetChain, " "), err)
	}

	forwardToSubGlobalNetChain = []string{"-j", constants.SmGlobalnetEgressChainForCluster}
	if err := util.InsertUnique(i.ipt, "nat", constants.SmGlobalnetEgressChain, 5, forwardToSubGlobalNetChain); err != nil {
		klog.Errorf("error inserting iptables rule %q: %v\n", strings.Join(forwardToSubGlobalNetChain, " "), err)
	}

	return nil
}

func (i *Controller) syncPodRules(podIP, globalIP string, addRules bool) error {
	err := i.updateEgressRulesForResource("Pod", podIP, globalIP, addRules)
	if err != nil {
		return fmt.Errorf("error updating egress rules for pod %s: %v", podIP, err)
	}

	return nil
}

func (i *Controller) syncServiceRules(service *k8sv1.Service, globalIP string, addRules bool) error {
	chainName, chainExists, err := i.kubeProxyClusterIPServiceChainName(service)
	if err != nil {
		return err
	}

	if !chainExists {
		// This shouldn't happen here as we check for this earlier.
		return nil
	}

	err = i.updateIngressRulesForService(globalIP, chainName, addRules)
	if err != nil {
		return fmt.Errorf("error updating ingress rules for service %#v: %v", service, err)
	}

	return nil
}

func (i *Controller) syncNodeRules(nodeName, cniIfaceIP, globalIP string, addRules bool) error {
	err := i.updateEgressRulesForResource("Node", cniIfaceIP, globalIP, addRules)
	if err != nil {
		return fmt.Errorf("error updating egress rules for Node %s: %v", cniIfaceIP, err)
	}

	// On the active Gateway Node where this code gets executed, we program icmp ingress rules
	// to support health-check use-case.
	if i.gwNodeName == nodeName {
		err = i.updateIngressRulesForHealthCheck("Node", cniIfaceIP, globalIP, addRules)
		if err != nil {
			return fmt.Errorf("error updating healthcheck ingress rules for Node %q: %v", nodeName, err)
		}
	}

	return nil
}

func (i *Controller) isServiceSupported(service *k8sv1.Service) bool {
	if service.Spec.Type != k8sv1.ServiceTypeClusterIP || service.Spec.ClusterIP == "None" {
		// Normally ClusterIPServices can be accessed only within the local cluster.
		// When multiple K8s clusters are connected via Submariner, it enables access
		// to ClusterIPService even from remote clusters. So, as part of Submariner
		// Globalnet implementation, we are only interested in ClusterIP Services and
		// not the other types of Services like LoadBalancer Services, NodePort Services
		// etc which are externally accessible.
		// TODO: Currently, Globalnet does not support headless services, hence we skip them here.
		return false
	}

	return true
}

func CreateGlobalNetMarkingChain(ipt iptables.Interface) error {
	klog.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetMarkChain)

	if err := util.CreateChainIfNotExists(ipt, "nat", constants.SmGlobalnetMarkChain); err != nil {
		return fmt.Errorf("error creating iptables chain %s: %v", constants.SmGlobalnetMarkChain, err)
	}

	return nil
}
