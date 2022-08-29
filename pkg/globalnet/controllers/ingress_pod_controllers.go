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

//nolint:dupl // same logic to ingress_endpoints_controllers, but for a different class
package controllers

import (
	"context"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/util"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"
)

func NewIngressPodControllers(config *syncer.ResourceSyncerConfig) (*IngressPodControllers, error) {
	// We'll panic if config is nil, this is intentional
	_, gvr, err := util.ToUnstructuredResource(&submarinerv1.GlobalIngressIP{}, config.RestMapper)
	if err != nil {
		return nil, errors.Wrap(err, "error converting resource")
	}

	return &IngressPodControllers{
		controllers: map[string]*ingressPodController{},
		config:      *config,
		ingressIPs:  config.SourceClient.Resource(*gvr),
	}, nil
}

func (c *IngressPodControllers) start(service *corev1.Service) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	key := c.key(service.Name, service.Namespace)
	if _, exists := c.controllers[key]; exists {
		return nil
	}

	controller, err := startIngressPodController(service, &c.config)
	if err != nil {
		return err
	}

	c.controllers[key] = controller

	return nil
}

func (c *IngressPodControllers) stopAll() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	for _, controller := range c.controllers {
		controller.Stop()
	}

	c.controllers = map[string]*ingressPodController{}
}

func (c *IngressPodControllers) stopAndCleanup(serviceName, serviceNamespace string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	key := c.key(serviceName, serviceNamespace)

	controller, exists := c.controllers[key]
	if exists {
		controller.Stop()
		delete(c.controllers, key)
	}

	svcSelector := labels.SelectorFromSet(map[string]string{ServiceRefLabel: serviceName}).String()
	err := c.ingressIPs.Namespace(serviceNamespace).DeleteCollection(context.TODO(), metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: svcSelector})

	if err != nil && !apierrors.IsNotFound(err) {
		klog.Errorf("Error deleting GlobalIngressIPs for service %q: %v", key, err)
	}
}

func (c *IngressPodControllers) key(n, ns string) string {
	return ns + "/" + n
}
