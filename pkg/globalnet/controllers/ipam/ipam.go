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
	"fmt"
	"sync"

	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/submariner/pkg/iptables"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func NewController(spec *SubmarinerIPAMControllerSpecification, config syncer.ResourceSyncerConfig,
	globalCIDR, gwNodeName string) (*Controller, error) {
	exclusionMap := make(map[string]bool)
	for _, v := range spec.ExcludeNS {
		exclusionMap[v] = true
	}

	pool, err := NewIPPool(globalCIDR)

	if err != nil {
		return nil, err
	}

	iptableHandler, err := iptables.New()
	if err != nil {
		return nil, err
	}

	ipamController := &Controller{
		serviceLock: sync.Mutex{},
		serviceClient: config.SourceClient.Resource(schema.GroupVersionResource{
			Group:    k8sv1.SchemeGroupVersion.Group,
			Version:  k8sv1.SchemeGroupVersion.Version,
			Resource: "services",
		}),
		scheme:            config.Scheme,
		gwNodeName:        gwNodeName,
		excludeNamespaces: exclusionMap,
		pool:              pool,
		ipt:               iptableHandler,
	}

	klog.Info("Setting up resource syncers")

	for _, t := range []runtime.Object{&k8sv1.Service{}, &mcsv1a1.ServiceExport{}, &k8sv1.Pod{}, &k8sv1.Node{}} {
		c := config
		c.Name = fmt.Sprintf("Syncer for %T", t)
		c.ResourceType = t

		switch t.(type) {
		case *k8sv1.Service:
			c.ResourcesEquivalent = ipamController.areGlobalIPsEquivalent
			c.Transform = ipamController.processService
		case *mcsv1a1.ServiceExport:
			c.Transform = ipamController.processServiceExport
		case *k8sv1.Pod:
			c.ShouldProcess = ipamController.checkExcludedNamespace
			c.ResourcesEquivalent = ipamController.onPodUpdated
			c.Transform = ipamController.processPod
		case *k8sv1.Node:
			c.ResourcesEquivalent = ipamController.onNodeUpdated
			c.Transform = ipamController.processNode
		}

		s, err := syncer.NewResourceSyncer(&c)
		if err != nil {
			return nil, err
		}

		ipamController.syncers = append(ipamController.syncers, s)
	}

	ipamController.serviceSyncer = ipamController.syncers[0]
	ipamController.serviceExportSyncer = ipamController.syncers[1]

	return ipamController, nil
}

func (i *Controller) Start(stopCh <-chan struct{}) error {
	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting IPAM Controller")

	err := i.initIPTableChains()
	if err != nil {
		return fmt.Errorf("initIPTableChains returned error. %v", err)
	}

	// Currently submariner global-net implementation works only with kube-proxy.
	if chainExists, _ := i.doesIPTablesChainExist("nat", kubeProxyServiceChainName); !chainExists {
		return fmt.Errorf("%q chain missing, cluster does not seem to use kube-proxy", kubeProxyServiceChainName)
	}

	klog.Infof("Starting %d resource syncers", len(i.syncers))

	for _, syncer := range i.syncers {
		err := syncer.Start(stopCh)
		if err != nil {
			return err
		}
	}

	return nil
}

func (i *Controller) checkExcludedNamespace(obj *unstructured.Unstructured, op syncer.Operation) bool {
	return !i.isInExcludedNamespace(obj)
}

func (i *Controller) isInExcludedNamespace(obj runtime.Object) bool {
	objMeta, _ := meta.Accessor(obj)

	if i.excludeNamespaces[objMeta.GetNamespace()] {
		klog.V(log.DEBUG).Infof("Skipping %s/%s as it belongs to excluded namespace",
			objMeta.GetNamespace(), objMeta.GetName())
		return true
	}

	return false
}

func (i *Controller) areGlobalIPsEquivalent(oldObj, newObj *unstructured.Unstructured) bool {
	key, _ := cache.MetaNamespaceKeyFunc(newObj)

	oldGlobalIP := oldObj.GetAnnotations()[SubmarinerIPAMGlobalIP]
	newGlobalIP := newObj.GetAnnotations()[SubmarinerIPAMGlobalIP]
	if oldGlobalIP != newGlobalIP && newGlobalIP != i.pool.GetAllocatedIP(key) {
		klog.V(log.DEBUG).Infof("GlobalIP changed from %s to %s for %q", oldGlobalIP, newGlobalIP, key)
		return false
	}

	return true
}

func (i *Controller) annotateGlobalIP(key, globalIP string) (string, error) {
	var ip string
	var err error
	if globalIP == "" {
		ip, err = i.pool.Allocate(key)
		if err != nil {
			return "", err
		}

		return ip, nil
	}

	givenIP, err := i.pool.RequestIP(key, globalIP)
	if err != nil {
		return "", err
	}

	if globalIP != givenIP {
		// This resource has been allocated a different IP
		klog.Warningf("Updating globalIP for %q from %s to %s", key, globalIP, givenIP)
		return givenIP, nil
	}
	// globalIP on the resource is now updated in the local pool
	// This case will be hit either when the gateway is migrated or when the globalNet Pod is restarted
	return "", nil
}

func (i *Controller) updateGlobalIP(obj runtime.Object, syncRules func(string) error) (runtime.Object, bool) {
	key, _ := cache.MetaNamespaceKeyFunc(obj)
	metaObj, _ := meta.Accessor(obj)

	existingGlobalIP := metaObj.GetAnnotations()[SubmarinerIPAMGlobalIP]
	allocatedIP, err := i.annotateGlobalIP(key, existingGlobalIP)
	if err != nil { // failed to get globalIP or failed to update, we want to retry
		klog.Errorf("Failed to get GlobalIP for pod %q: %v", key, err)
		return nil, true
	}

	// This case is hit in one of the two situations
	// 1. when the resource does not have the globalIP annotation and a new globalIP is allocated
	// 2. when the current globalIP annotation on the resource does not match with the info maintained by ipPool
	if allocatedIP != "" {
		klog.V(log.DEBUG).Infof("Allocating globalIP %s to %T %q ", allocatedIP, obj, key)
		err = syncRules(allocatedIP)
		if err != nil {
			return nil, true
		}

		annotations := metaObj.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}

		annotations[SubmarinerIPAMGlobalIP] = allocatedIP
		metaObj.SetAnnotations(annotations)

		return obj, false
	} else if existingGlobalIP != "" {
		klog.V(log.DEBUG).Infof("%T %q already has globalIP %s annotation, syncing", obj, key, existingGlobalIP)
		// When Globalnet Controller is migrated, we get notification for all the existing resources.
		// For resources that already have the annotation, we update the local ipPool cache and sync
		// the iptable rules on the new GatewayNode.
		// Note: This case will also be hit when Globalnet Pod is restarted
		err = syncRules(existingGlobalIP)
		if err != nil {
			return nil, true
		}
	}

	return nil, false
}

func (i *Controller) fromUnstructured(from *unstructured.Unstructured, to runtime.Object) runtime.Object {
	err := i.scheme.Convert(from, to, nil)
	if err != nil {
		klog.Fatalf("Error converting %#v to %T: %v", from, to, err)
	}

	return to
}
