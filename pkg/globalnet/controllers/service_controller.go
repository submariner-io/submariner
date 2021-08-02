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

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/util"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers/iptables"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

func NewServiceController(config syncer.ResourceSyncerConfig, podControllers *IngressPodControllers) (Interface, error) {
	var err error

	klog.Info("Creating Service controller")

	controller := &serviceController{
		baseSyncerController: newBaseSyncerController(),
		podControllers:       podControllers,
	}

	controller.resourceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "Service syncer",
		ResourceType:    &corev1.Service{},
		SourceClient:    config.SourceClient,
		SourceNamespace: corev1.NamespaceAll,
		RestMapper:      config.RestMapper,
		Federator:       federate.NewUpdateFederator(config.SourceClient, config.RestMapper, corev1.NamespaceAll),
		Scheme:          config.Scheme,
		Transform:       controller.process,
	})

	if err != nil {
		return nil, err
	}

	iptIface, err := iptables.New()
	if err != nil {
		return nil, errors.WithMessage(err, "error creating the IPTablesInterface handler")
	}

	controller.iptIface = iptIface

	_, gvr, err := util.ToUnstructuredResource(&submarinerv1.GlobalIngressIP{}, config.RestMapper)
	if err != nil {
		return nil, err
	}

	controller.ingressIPs = config.SourceClient.Resource(*gvr)

	return controller, nil
}

func (c *serviceController) Start() error {
	err := c.baseSyncerController.Start()
	if err != nil {
		return err
	}

	c.reconcile(c.ingressIPs.Namespace(corev1.NamespaceAll), func(obj *unstructured.Unstructured) runtime.Object {
		name, exists, _ := unstructured.NestedString(obj.Object, "spec", "serviceRef", "name")
		if exists {
			return &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: obj.GetNamespace(),
				},
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeClusterIP,
				},
			}
		}

		return nil
	})

	return nil
}

func (c *serviceController) process(from runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	service := from.(*corev1.Service)

	if service.Spec.Type != corev1.ServiceTypeClusterIP {
		return nil, false
	}

	if op == syncer.Delete {
		return c.onDelete(service)
	}

	return nil, false
}

func (c *serviceController) onDelete(service *corev1.Service) (runtime.Object, bool) {
	key, _ := cache.MetaNamespaceKeyFunc(service)

	klog.Infof("Service %q deleted", key)

	c.podControllers.stopAndCleanup(service.Name, service.Namespace)

	if service.Spec.ClusterIP == corev1.ClusterIPNone {
		return nil, false
	}

	ingressIP, err := c.ingressIPs.Namespace(service.Namespace).Get(context.TODO(), service.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, false
	}

	if err != nil {
		klog.Warningf("Error retrieving the GlobalIngressObject for service %q: %v", key, err)
		return nil, false
	}

	globalIP, ok, _ := unstructured.NestedString(ingressIP.Object, "status", "allocatedIP")
	if ok && globalIP != "" {
		return nil, false
	}

	// In the current implementation Globalnet relies on the iptables-chain created by kube-proxy for
	// supporting services. So, when a service is deleted, the sooner we delete any references to the
	// iptable-chain its better, otherwise we might be blocking kubeproxy from deleting the respective
	// service chain. Usually in such situations, kubeproxy will log an error message and continue to
	// retry until all references are deleted. Here, we are trying to delete the respective ingress rules
	// as soon as service is deleted to avoid such situations. This approach can be modified once the
	// following issue is addressed - https://github.com/submariner-io/submariner/issues/1166
	chainName := ingressIP.GetAnnotations()[kubeProxyIPTableChainAnnotation]
	if chainName != "" {
		// Ignore any errors, it will again be retried when the globalIngressIP object is deleted.
		_ = c.iptIface.RemoveIngressRulesForService(globalIP, chainName)
	}

	podIP := ingressIP.GetAnnotations()[headlessSvcPodIP]
	if podIP != "" {
		// Ignore any errors, it will again be retried when the globalIngressIP object is deleted.
		_ = c.iptIface.RemoveIngressRulesForHeadlessSvcPod(globalIP, podIP)
	}

	return &submarinerv1.GlobalIngressIP{
		ObjectMeta: metav1.ObjectMeta{
			Name:      service.Name,
			Namespace: service.Namespace,
		},
	}, false
}
