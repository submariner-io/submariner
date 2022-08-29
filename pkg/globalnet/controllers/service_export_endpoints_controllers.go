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
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/util"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func NewServiceExportEndpointsControllers(config *syncer.ResourceSyncerConfig) (*ServiceExportEndpointsControllers, error) {
	// We'll panic if config is nil, this is intentional
	// Ensure that cloned endpoints whose original endpoints are deleted while the controller stops are deleted.
	_, gvr, err := util.ToUnstructuredResource(&corev1.Endpoints{}, config.RestMapper)
	if err != nil {
		return nil, errors.Wrap(err, "error converting resource")
	}

	client := config.SourceClient.Resource(*gvr)

	err = ensureClonedHasOriginal(client)
	if err != nil {
		return nil, errors.Wrap(err, "error ensuring cloned Endpoints has original")
	}

	return &ServiceExportEndpointsControllers{
		controllers: map[string]*endpointsController{},
		config:      *config,
	}, nil
}

func (c *ServiceExportEndpointsControllers) start(se *mcsv1a1.ServiceExport) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	key := c.key(se.Name, se.Namespace)
	if _, exists := c.controllers[key]; exists {
		return nil
	}

	controller, err := startEndpointsController(se.Name, se.Namespace, &c.config)
	if err != nil {
		return err
	}

	c.controllers[key] = controller

	return nil
}

func (c *ServiceExportEndpointsControllers) stopAll() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	for _, controller := range c.controllers {
		controller.Stop()
	}

	c.controllers = map[string]*endpointsController{}
}

func (c *ServiceExportEndpointsControllers) stopAndCleanup(seName, seNamespace string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	key := c.key(seName, seNamespace)

	controller, exists := c.controllers[key]
	if exists {
		controller.Stop()
		delete(c.controllers, key)
	}
}

func ensureClonedHasOriginal(client dynamic.NamespaceableResourceInterface) error {
	list, err := client.Namespace(corev1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "error listing the resources")
	}

	for _, obj1 := range list.Items {
		var (
			found, ok bool
			origEp    string
		)

		labels := obj1.GetLabels()
		if origEp, ok = labels[constants.EndpointClonedFrom]; !ok {
			// Not a cloned Endpoints.
			continue
		}

		for _, obj2 := range list.Items {
			if obj1.GetNamespace() == obj2.GetNamespace() && origEp == obj2.GetName() {
				found = true
				break
			}
		}

		if found {
			// Original Endpoints found.
			continue
		}

		// Delete the cloned Endpoints that doesn't have original Endpoints.
		klog.Infof("Deleting cloned Endpoints %s/%s that doesn't have original Endpoints %s/%s",
			obj1.GetNamespace(), obj1.GetName(), obj1.GetNamespace(), origEp)

		err := deleteEndpoints(obj1.GetNamespace(), obj1.GetName(), client)
		if err != nil {
			return errors.Wrap(err, "error deleting cloned Endpoints without original")
		}
	}

	return nil
}

func (c *ServiceExportEndpointsControllers) key(n, ns string) string {
	return ns + "/" + n
}
