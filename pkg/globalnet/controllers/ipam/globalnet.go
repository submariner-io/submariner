package ipam

import (
	"fmt"
	"strings"

	"github.com/coreos/go-iptables/iptables"
	"github.com/submariner-io/admiral/pkg/log"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/routeagent/constants"
	"github.com/submariner-io/submariner/pkg/routeagent/controllers/route"
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
	chainName := i.kubeProxyClusterIpServiceChainName(service)
	err := i.updateIngressRulesForService(globalIP, chainName, addRules)
	if err != nil {
		return fmt.Errorf("error updating ingress rules for service %#v: %v", service, err)
	}

	return nil
}

func (i *Controller) syncNodeRules(cniIfaceIP, globalIP string, addRules bool) error {
	err := i.updateEgressRulesForResource("Node", cniIfaceIP, globalIP, addRules)
	if err != nil {
		return fmt.Errorf("error updating egress rules for Node %s: %v", cniIfaceIP, err)
	}

	return nil
}

func (i *Controller) evaluateService(service *k8sv1.Service) Operation {
	if service.Spec.Type != k8sv1.ServiceTypeClusterIP {
		// Normally ClusterIPServices can be accessed only within the local cluster.
		// When multiple K8s clusters are connected via Submariner, it enables access
		// to ClusterIPService even from remote clusters. So, as part of Submariner
		// Globalnet implementation, we are only interested in ClusterIP Services and
		// not the other types of Services like LoadBalancer Services, NodePort Services
		// etc which are externally accessible.
		return Ignore
	}

	chainName := i.kubeProxyClusterIpServiceChainName(service)
	if chainExists, _ := i.doesIPTablesChainExist("nat", chainName); !chainExists {
		return Requeue
	}

	serviceName := service.GetNamespace() + "/" + service.GetName()
	klog.V(log.DEBUG).Infof("kube-proxy chain %q for service %q now exists.", chainName, serviceName)

	return Process
}

func (i *Controller) evaluateNode(node *k8sv1.Node) Operation {
	cniIfaceIP := node.GetAnnotations()[route.CniInterfaceIp]
	if cniIfaceIP == "" {
		// To support connectivity from HostNetwork to remoteCluster, globalnet requires the
		// cniIfaceIP of the respective node. Route-agent running on the node annotates the
		// respective node with the cniIfaceIP. In this API, we check for the presence of this
		// annotation and process the node event only when the annotation exists.
		return Requeue
	}

	return Process
}

func CreateGlobalNetMarkingChain(ipt *iptables.IPTables) error {
	klog.V(log.DEBUG).Infof("Install/ensure %s chain exists", constants.SmGlobalnetMarkChain)

	if err := util.CreateChainIfNotExists(ipt, "nat", constants.SmGlobalnetMarkChain); err != nil {
		return fmt.Errorf("error creating iptables chain %s: %v", constants.SmGlobalnetMarkChain, err)
	}

	return nil
}
