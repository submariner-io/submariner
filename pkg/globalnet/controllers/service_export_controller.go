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
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func NewServiceExportController(config *syncer.ResourceSyncerConfig, podControllers *IngressPodControllers) (Interface, error) {
	// We'll panic if config is nil, this is intentional
	var err error

	klog.Info("Creating ServiceExport controller")

	_, gvr, err := util.ToUnstructuredResource(&corev1.Service{}, config.RestMapper)
	if err != nil {
		return nil, err
	}

	controller := &serviceExportController{
		baseSyncerController: newBaseSyncerController(),
		services:             config.SourceClient.Resource(*gvr),
		podControllers:       podControllers,
		scheme:               config.Scheme,
	}

	controller.resourceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "ServiceExport syncer",
		ResourceType:    &mcsv1a1.ServiceExport{},
		SourceClient:    config.SourceClient,
		SourceNamespace: corev1.NamespaceAll,
		RestMapper:      config.RestMapper,
		Federator:       federate.NewCreateFederator(config.SourceClient, config.RestMapper, corev1.NamespaceAll),
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

	_, gvr, err = util.ToUnstructuredResource(&submarinerv1.GlobalIngressIP{}, config.RestMapper)
	if err != nil {
		return nil, err
	}

	controller.ingressIPs = config.SourceClient.Resource(*gvr).Namespace(corev1.NamespaceAll)

	return controller, nil
}

func (c *serviceExportController) Stop() {
	c.baseController.Stop()
	c.podControllers.stopAll()
}

func (c *serviceExportController) Start() error {
	err := c.baseSyncerController.Start()
	if err != nil {
		return err
	}

	c.reconcile(c.ingressIPs, func(obj *unstructured.Unstructured) runtime.Object {
		name, exists, _ := unstructured.NestedString(obj.Object, "spec", "serviceRef", "name")
		if exists {
			return &mcsv1a1.ServiceExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: obj.GetNamespace(),
				},
			}
		}

		return nil
	})

	return nil
}

func (c *serviceExportController) process(from runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	serviceExport := from.(*mcsv1a1.ServiceExport)

	switch op {
	case syncer.Create:
		return c.onCreate(serviceExport)
	case syncer.Delete:
		return c.onDelete(serviceExport)
	case syncer.Update:
	}

	return nil, false
}

func (c *serviceExportController) onCreate(serviceExport *mcsv1a1.ServiceExport) (runtime.Object, bool) {
	key, _ := cache.MetaNamespaceKeyFunc(serviceExport)

	service, exists, err := c.getService(serviceExport.Name, serviceExport.Namespace)
	if err != nil || !exists {
		klog.Infof("Exported Service %q does not exist yet - re-queueing", key)
		return nil, true
	}

	if service.Spec.Type != corev1.ServiceTypeClusterIP {
		klog.Infof("Exported Service %q with type %q is not supported", key, service.Spec.Type)

		return nil, false
	}

	klog.Infof("Processing ServiceExport %q", key)

	if service.Spec.ClusterIP == corev1.ClusterIPNone {
		// Headless service
		return c.onCreateHeadless(key, service)
	}

	chainName, chainExists, err := c.iptIface.GetKubeProxyClusterIPServiceChainName(service, kubeProxyServiceChainPrefix)
	if err != nil {
		klog.Errorf("Error getting kube-proxy chain name for service %q: %v", key, err)
		return nil, true
	}

	if !chainExists {
		klog.Infof("Kubeproxy chain for service %q does not exist yet", key)
		return nil, true
	}

	ingressIP := &submarinerv1.GlobalIngressIP{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceExport.Name,
			Namespace: serviceExport.Namespace,
			Annotations: map[string]string{
				kubeProxyIPTableChainAnnotation: chainName,
			},
		},
		Spec: submarinerv1.GlobalIngressIPSpec{
			Target:     submarinerv1.ClusterIPService,
			ServiceRef: &corev1.LocalObjectReference{Name: serviceExport.Name},
		},
	}

	klog.Infof("Creating %#v", ingressIP)

	return ingressIP, false
}

func (c *serviceExportController) onDelete(serviceExport *mcsv1a1.ServiceExport) (runtime.Object, bool) {
	key, _ := cache.MetaNamespaceKeyFunc(serviceExport)

	klog.Infof("ServiceExport %q deleted", key)

	c.podControllers.stopAndCleanup(serviceExport.Name, serviceExport.Namespace)

	return &submarinerv1.GlobalIngressIP{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceExport.Name,
			Namespace: serviceExport.Namespace,
		},
	}, false
}

func (c *serviceExportController) getService(name, namespace string) (*corev1.Service, bool, error) {
	obj, err := c.services.Namespace(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, false, nil
	}

	if err != nil {
		klog.Errorf("Error retrieving Service %s/%s: %v", namespace, name, err)
		return nil, false, err
	}

	service := &corev1.Service{}
	err = c.scheme.Convert(obj, service, nil)
	if err != nil {
		klog.Errorf("Error converting %#v to Service: %v", obj, err)
		return nil, false, err
	}

	return service, true, nil
}

func (c *serviceExportController) onCreateHeadless(key string, service *corev1.Service) (runtime.Object, bool) {
	err := c.podControllers.start(service)
	if err != nil {
		klog.Errorf("Failed to create pod controller for service %q", key)
		return nil, true
	}

	return nil, false
}
