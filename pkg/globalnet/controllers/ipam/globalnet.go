package ipam

import (
	k8sv1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog"
)

func (i *Controller) syncPodRules(podIP, globalIP string, addOrDelete Operation) {
	err := i.updateEgressRulesForPod(podIP, globalIP, addOrDelete)
	if err != nil {
		klog.Errorf("updateEgressRulesForPod returned error. %v", err)
		return
	}
}

func (i *Controller) syncServiceRules(service *k8sv1.Service, globalIP string, addOrDelete Operation) {
	chainName := i.kubeProxyClusterIpServiceChainName(service)
	err := i.updateIngressRulesForService(globalIP, chainName, addOrDelete)
	if err != nil {
		klog.Errorf("updateIngressRulesForService returned error. %v", err)
		return
	}
}

func (i *Controller) processServiceStatus(service *k8sv1.Service) Operation {
	if service.Spec.Type != v1.ServiceTypeClusterIP {
		// Normally ClusterIPServices can be accessed only within the local cluster.
		// When multiple K8s clusters are connected via Submariner, it enables access
		// to ClusterIPService even from remote clusters. So, as part of Submariner
		// Globalnet implementation, we are only interested in ClusterIP Services and
		// not the other types of Services like LoadBalancer Services, NodePort Services
		// etc which are externally accessible.
		return Ignore
	}

	if len(service.Spec.Selector) != 0 {
		chainName := i.kubeProxyClusterIpServiceChainName(service)
		if i.doesIPTablesChainExist("nat", chainName) != nil {
			return Requeue
		}
	} else {
		// Ignore services that do not have selectors
		return Ignore
	}
	return Process
}
