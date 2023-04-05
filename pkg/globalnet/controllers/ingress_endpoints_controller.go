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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

func startIngressEndpointsController(svc *corev1.Service, config *syncer.ResourceSyncerConfig) (*ingressEndpointsController, error) {
	var err error

	_, gvr, err := util.ToUnstructuredResource(&submarinerv1.GlobalIngressIP{}, config.RestMapper)
	if err != nil {
		return nil, errors.Wrap(err, "error converting resource")
	}

	controller := &ingressEndpointsController{
		baseSyncerController: newBaseSyncerController(),
		svcName:              svc.Name,
		namespace:            svc.Namespace,
		config:               *config,
		ingressIPs:           config.SourceClient.Resource(*gvr),
	}

	fieldSelector := fields.Set(map[string]string{"metadata.name": svc.Name}).AsSelector().String()

	controller.resourceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:                "Ingress Endpoints syncer",
		ResourceType:        &corev1.Endpoints{},
		SourceClient:        config.SourceClient,
		SourceNamespace:     svc.Namespace,
		RestMapper:          config.RestMapper,
		Federator:           federate.NewCreateFederator(config.SourceClient, config.RestMapper, corev1.NamespaceAll),
		Scheme:              config.Scheme,
		Transform:           controller.process,
		SourceFieldSelector: fieldSelector,
		ResourcesEquivalent: areEndpointsEqual,
	})

	if err != nil {
		return nil, errors.Wrap(err, "error creating the endpoints syncer")
	}

	if err := controller.Start(); err != nil {
		return nil, errors.Wrap(err, "error starting the endpoint syncer")
	}

	ingressIPs := config.SourceClient.Resource(*gvr).Namespace(corev1.NamespaceAll)
	controller.reconcile(ingressIPs, "" /* labelSelector */, fieldSelector, func(obj *unstructured.Unstructured) runtime.Object {
		endpointsName, exists, _ := unstructured.NestedString(obj.Object, "spec", "serviceRef", "name")
		if exists {
			return &corev1.Endpoints{
				ObjectMeta: metav1.ObjectMeta{
					Name:      endpointsName,
					Namespace: obj.GetNamespace(),
				},
			}
		}

		return nil
	})

	logger.Infof("Created Endpoints controller for (%s/%s) with selector %q", svc.Namespace, svc.Name, fieldSelector)

	return controller, nil
}

func (c *ingressEndpointsController) process(from runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	endpoints := from.(*corev1.Endpoints)

	switch op {
	case syncer.Create, syncer.Update:
		return c.onCreateOrUpdate(endpoints, op)
	case syncer.Delete:
		return c.onDelete(endpoints)
	}

	return nil, false
}

func (c *ingressEndpointsController) onCreateOrUpdate(endpoints *corev1.Endpoints, op syncer.Operation) (runtime.Object, bool) {
	key, _ := cache.MetaNamespaceKeyFunc(endpoints)

	logger.Infof("%q ingress Endpoints %s for service %s", op, key, c.svcName)

	// Get list of current endpoint IPs
	usedEpIPs := map[string]bool{}

	for _, subset := range endpoints.Subsets {
		for _, addr := range subset.Addresses {
			usedEpIPs[addr.IP] = true
		}
	}

	// Get List of existing ingressIPs
	selector := labels.SelectorFromSet(map[string]string{ServiceRefLabel: endpoints.Name}).String()

	existingIngressIPs, err := c.ingressIPs.Namespace(endpoints.Namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, true
	}

	// Delete unused ingressIPs
	for _, ingressIP := range existingIngressIPs.Items {
		annotations := ingressIP.GetAnnotations()
		if epIP, ok := annotations[headlessSvcEndpointsIP]; ok {
			// ingressIP is still used
			if _, used := usedEpIPs[epIP]; used {
				// Delete from the hash to avoid recreating existing ingressIP
				delete(usedEpIPs, epIP)
				// Skip deleting ingressIP
				continue
			}
		}

		err := c.ingressIPs.Namespace(ingressIP.GetNamespace()).Delete(context.TODO(), ingressIP.GetName(), metav1.DeleteOptions{})
		if err != nil {
			return nil, true
		}
	}

	// Create new ingressIPs
	for epIP := range usedEpIPs {
		ingressIP := newIngressIP(endpoints.Name, endpoints.Namespace, epIP)

		ingressIP.ObjectMeta.Annotations = map[string]string{
			headlessSvcEndpointsIP: epIP,
		}

		ingressIP.Spec = submarinerv1.GlobalIngressIPSpec{
			Target:     submarinerv1.HeadlessServiceEndpoints,
			ServiceRef: &corev1.LocalObjectReference{Name: c.svcName},
		}

		uIngressIP, _, err := util.ToUnstructuredResource(ingressIP, c.config.RestMapper)
		if err != nil {
			return nil, true
		}

		_, err = c.ingressIPs.Namespace(ingressIP.GetNamespace()).Create(context.TODO(), uIngressIP, metav1.CreateOptions{})
		if err != nil {
			return nil, true
		}
	}

	return nil, false
}

func (c *ingressEndpointsController) onDelete(endpoints *corev1.Endpoints) (runtime.Object, bool) {
	key, _ := cache.MetaNamespaceKeyFunc(endpoints)

	selector := labels.SelectorFromSet(map[string]string{ServiceRefLabel: endpoints.Name}).String()

	err := c.ingressIPs.Namespace(endpoints.Namespace).DeleteCollection(context.TODO(), metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, true
	}

	logger.Infof("Ingress Endpoints %s for service %s deleted", key, c.svcName)

	return nil, false
}

func newIngressIP(name, namespace, epIP string) *submarinerv1.GlobalIngressIP {
	return &submarinerv1.GlobalIngressIP{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("ep-%.44s-%.15s", name, epIP),
			Namespace: namespace,
			Labels: map[string]string{
				ServiceRefLabel: name,
			},
		},
	}
}
