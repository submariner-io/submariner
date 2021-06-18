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
package controllers

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/util"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers/iptables"
	"github.com/submariner-io/submariner/pkg/ipam"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

func NewGlobalIngressIPController(config syncer.ResourceSyncerConfig, pool *ipam.IPPool) (Interface, error) {
	var err error

	klog.Info("Creating GlobalIngressIP controller")

	iptIface, err := iptables.New()
	if err != nil {
		return nil, errors.WithMessage(err, "error creating the IPTablesInterface handler")
	}

	controller := &globalIngressIPController{
		baseIPAllocationController: newBaseIPAllocationController(pool, iptIface),
	}

	_, gvr, err := util.ToUnstructuredResource(&submarinerv1.GlobalIngressIP{}, config.RestMapper)
	if err != nil {
		return nil, err
	}

	federator := federate.NewUpdateFederator(config.SourceClient, config.RestMapper, corev1.NamespaceAll)

	client := config.SourceClient.Resource(*gvr).Namespace(corev1.NamespaceAll)
	list, err := client.List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	for i := range list.Items {
		obj := &list.Items[i]

		chainName := obj.GetAnnotations()[kubeProxyIPTableChainAnnotation]
		if chainName == "" {
			continue
		}

		err = controller.reserveAllocatedIPs(federator, obj, func(reservedIPs []string) error {
			return controller.iptIface.AddIngressRulesForService(reservedIPs[0], chainName)
		})

		if err != nil {
			return nil, err
		}
	}

	controller.resourceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:                "GlobalIngressIP syncer",
		ResourceType:        &submarinerv1.GlobalIngressIP{},
		SourceClient:        config.SourceClient,
		SourceNamespace:     corev1.NamespaceAll,
		RestMapper:          config.RestMapper,
		Federator:           federator,
		Scheme:              config.Scheme,
		Transform:           controller.process,
		ResourcesEquivalent: syncer.AreSpecsEquivalent,
	})

	if err != nil {
		return nil, err
	}

	// TODO - reconcile existing items to handle ServiceExport deleted while we were down

	return controller, nil
}

func (c *globalIngressIPController) process(from runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	ingressIP := from.(*submarinerv1.GlobalIngressIP)

	klog.Infof("Processing %sd %#v", op, ingressIP)

	switch op {
	case syncer.Create:
		prevStatus := ingressIP.Status
		requeue := c.onCreate(ingressIP)

		return checkStatusChanged(&prevStatus, &ingressIP.Status, ingressIP), requeue
	case syncer.Delete:
		return c.onDelete(ingressIP)
	}

	return nil, false
}

func (c *globalIngressIPController) onCreate(ingressIP *submarinerv1.GlobalIngressIP) bool {
	// If Ingress GlobalIP is already allocated, simply return.
	if ingressIP.Status.AllocatedIP != "" {
		return false
	}

	key, _ := cache.MetaNamespaceKeyFunc(ingressIP)

	klog.Infof("Allocating global IP for %q", key)

	ips, err := c.pool.Allocate(1)
	if err != nil {
		klog.Errorf("Error allocating IP for %q: %v", key, err)
		tryAppendStatusCondition(&ingressIP.Status.Conditions, &metav1.Condition{
			Type:    string(submarinerv1.GlobalEgressIPAllocated),
			Status:  metav1.ConditionFalse,
			Reason:  "IPPoolAllocationFailed",
			Message: fmt.Sprintf("Error allocating a global IP from the pool: %v", err),
		})

		return true
	}

	if ingressIP.Spec.Target == submarinerv1.ClusterIPService {
		chainName := ingressIP.GetAnnotations()[kubeProxyIPTableChainAnnotation]
		if chainName == "" {
			klog.Warningf("%q annotation is missing on %q", kubeProxyIPTableChainAnnotation, key)

			if err := c.pool.Release(ips...); err != nil {
				klog.Errorf("Error while releasing the global IPs: %v", err)
			}

			return true
		}

		err = c.iptIface.AddIngressRulesForService(ips[0], chainName)
		if err != nil {
			klog.Errorf("Error while programming Service %q ingress rules. %v", key, err)

			if err := c.pool.Release(ips...); err != nil {
				klog.Errorf("Sync rules failed and error while releasing the global IPs: %v", err)
			}

			return true
		}
	}

	ingressIP.Status.AllocatedIP = ips[0]

	tryAppendStatusCondition(&ingressIP.Status.Conditions, &metav1.Condition{
		Type:    string(submarinerv1.GlobalEgressIPAllocated),
		Status:  metav1.ConditionTrue,
		Reason:  "Success",
		Message: "Allocated global IP",
	})

	return false
}

func (c *globalIngressIPController) onDelete(ingressIP *submarinerv1.GlobalIngressIP) (runtime.Object, bool) {
	if ingressIP.Status.AllocatedIP == "" {
		return nil, false
	}

	key, _ := cache.MetaNamespaceKeyFunc(ingressIP)

	if ingressIP.Spec.Target == submarinerv1.ClusterIPService {
		chainName := ingressIP.GetAnnotations()[kubeProxyIPTableChainAnnotation]
		if chainName != "" {
			err := c.iptIface.RemoveIngressRulesForService(ingressIP.Status.AllocatedIP, chainName)
			if err != nil {
				klog.Errorf("Error while deleting Service %q ingress rules. %v", key, err)
			}
		} else {
			klog.Warningf("%q annotation is missing on %q", kubeProxyIPTableChainAnnotation, key)
		}
	}

	err := c.pool.Release(ingressIP.Status.AllocatedIP)
	if err != nil {
		klog.Errorf("Error releasing IP %s for GlobalIngressIP %q", ingressIP.Status.AllocatedIP, key)
	}

	return ingressIP, false
}
