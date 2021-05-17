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
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

func (i *Controller) shouldProcessService(service *k8sv1.Service) bool {
	if !i.isServiceSupported(service) {
		klog.V(log.DEBUG).Infof("In shouldProcessService, skipping Service %s/%s", service.GetNamespace(), service.GetName())
		return false
	}

	return !i.isInExcludedNamespace(service)
}

func (i *Controller) processService(from runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	service := from.(*k8sv1.Service)

	if !i.shouldProcessService(service) {
		return nil, false
	}

	i.serviceLock.Lock()
	defer i.serviceLock.Unlock()

	if op == syncer.Delete {
		i.processRemovedService(service)
		return nil, false
	}

	serviceName := service.GetNamespace() + "/" + service.GetName()

	_, exists, err := i.serviceExportSyncer.GetResource(service.Name, service.Namespace)
	if err != nil {
		klog.Errorf("Failed to get ServiceExport for %q: %v", serviceName, err)
		return nil, true
	}

	if !exists {
		return nil, false
	}

	chainName, chainExists, err := i.kubeProxyClusterIPServiceChainName(service)
	if err != nil || !chainExists {
		if err != nil {
			klog.Errorf("Error checking for kube-proxy chain for service %q", serviceName)
		}

		// If the kubeproxy chain for the service is missing on the node (might happen if there is
		// some issue with kubeproxy pod itself or if using non-iptables based kubeproxy), instead
		// of re-queuing forever, we place a hard-limit of maxServiceRequeues.
		if numRequeues >= maxServiceRequeues {
			klog.Warningf("Service %s requeued max(%q) allowed iterations, ignoring it",
				serviceName, maxServiceRequeues)
			return nil, false
		}

		return nil, true
	}

	klog.V(log.DEBUG).Infof("kube-proxy chain %q for service %q exists.", chainName, serviceName)

	return i.updateService(service)
}

func (i *Controller) processRemovedService(service *k8sv1.Service) {
	globalIP := service.Annotations[SubmarinerIPAMGlobalIP]
	if globalIP != "" {
		key, _ := cache.MetaNamespaceKeyFunc(service)

		i.pool.Release(key)

		klog.V(log.DEBUG).Infof("Released ip %s for service %s", globalIP, key)

		err := i.syncServiceRules(service, globalIP, DeleteRules)
		if err != nil {
			klog.Errorf("Error while cleaning up Service %q ingress rules. %v", key, err)
		}
	}
}

func (i *Controller) updateService(service *k8sv1.Service) (runtime.Object, bool) {
	return i.updateGlobalIP(service, func(globalIP string) error {
		return i.syncServiceRules(service, globalIP, AddRules)
	})
}
