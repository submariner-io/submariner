package ipam

import (
	k8sv1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog"
)

func (i *Controller) syncGlobalNetRulesForPod(podIP, globalIP string, addOrDelete Operation) {
	err := i.updateEgressRulesForPod(podIP, globalIP, addOrDelete)
	if err != nil {
		klog.Errorf("updateEgressRulesForPod returned error. %v", err)
		return
	}
}

func (i *Controller) syncGlobalNetRulesForService(service *k8sv1.Service, globalIP string, addOrDelete Operation) {
	chainName := i.kubeProxyClusterIpServiceChainName(service)
	err := i.updateIngressRulesForService(globalIP, chainName, addOrDelete)
	if err != nil {
		klog.Errorf("updateIngressRulesForService returned error. %v", err)
		return
	}
}

func (i *Controller) processServiceStatus(service *k8sv1.Service) Operation {
	if service.Spec.Type != v1.ServiceTypeClusterIP {
		// We are only interested in ClusterIP Services.
		return Ignore
	}

	if len(service.Spec.Selector) != 0 {
		chainName := i.kubeProxyClusterIpServiceChainName(service)
		if i.doesIptablesChainExist("nat", chainName) != nil {
			return Requeue
		}
	} else {
		// Ignore services that do not have selectors
		return Ignore
	}
	return Process
}
