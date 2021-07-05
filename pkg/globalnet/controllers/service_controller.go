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
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/syncer"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	return controller, nil
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

	if service.Spec.ClusterIP == corev1.ClusterIPNone {
		c.podControllers.stopAndCleanup(service.Name, service.Namespace)
		return nil, false
	}

	return &submarinerv1.GlobalIngressIP{
		ObjectMeta: metav1.ObjectMeta{
			Name:      service.Name,
			Namespace: service.Namespace,
		},
	}, false
}
