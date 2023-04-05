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
	"reflect"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/util"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

func startEndpointsController(name, namespace string, config *syncer.ResourceSyncerConfig) (*endpointsController, error) {
	// We'll panic if config is nil, this is intentional
	var err error

	logger.Infof("Creating Endpoints controller for %s/%s", namespace, name)

	_, gvr, err := util.ToUnstructuredResource(&corev1.Endpoints{}, config.RestMapper)
	if err != nil {
		return nil, errors.Wrap(err, "error converting resource")
	}

	controller := &endpointsController{
		baseSyncerController: newBaseSyncerController(),
		name:                 name,
		namespace:            namespace,
		endpoints:            config.SourceClient.Resource(*gvr),
	}

	fieldSelector := fields.Set(map[string]string{"metadata.name": name}).AsSelector().String()

	controller.resourceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:                "Endpoints syncer",
		ResourceType:        &corev1.Endpoints{},
		SourceClient:        config.SourceClient,
		SourceNamespace:     namespace,
		RestMapper:          config.RestMapper,
		Federator:           federate.NewCreateOrUpdateFederator(config.SourceClient, config.RestMapper, namespace, "" /* localClusterID */),
		Transform:           controller.process,
		SourceFieldSelector: fieldSelector,
		ResourcesEquivalent: areEndpointsEqual,
	})

	if err != nil {
		return nil, errors.Wrap(err, "error creating the syncer")
	}

	if err := controller.Start(); err != nil {
		return nil, err
	}

	return controller, nil
}

func (c *endpointsController) Stop() {
	c.baseController.Stop()
}

func (c *endpointsController) Start() error {
	err := c.baseSyncerController.Start()
	if err != nil {
		return err
	}

	return nil
}

func (c *endpointsController) process(from runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	ep := from.(*corev1.Endpoints)

	switch op {
	case syncer.Create, syncer.Update:
		return c.onCreateOrUpdate(ep)
	case syncer.Delete:
		return c.onDelete(ep)
	}

	return nil, false
}

func (c *endpointsController) onCreateOrUpdate(ep *corev1.Endpoints) (runtime.Object, bool) {
	key, _ := cache.MetaNamespaceKeyFunc(ep)

	logger.Infof("Processing Endpoints %q", key)

	// Skip cloning already cloned Endpoints resource
	if _, ok := ep.Labels[constants.EndpointClonedFrom]; ok {
		logger.Infof("Skip cloning already cloned Endpoints %q", key)

		return ep, false
	}

	clonedEp := &corev1.Endpoints{}
	clonedEp.Namespace = ep.Namespace
	clonedEp.Name = GetInternalSvcName(ep.Name)
	clonedEp.Labels = map[string]string{constants.EndpointClonedFrom: ep.Name}
	clonedEp.Subsets = ep.Subsets

	return clonedEp, false
}

func (c *endpointsController) onDelete(ep *corev1.Endpoints) (runtime.Object, bool) {
	key, _ := cache.MetaNamespaceKeyFunc(ep)

	logger.Infof("Endpoints %q deleted", key)

	return &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      GetInternalSvcName(ep.Name),
			Namespace: ep.Namespace,
		},
	}, false
}

func areEndpointsEqual(obj1, obj2 *unstructured.Unstructured) bool {
	subsets1, _, _ := unstructured.NestedSlice(obj1.Object, "subsets")
	subsets2, _, _ := unstructured.NestedSlice(obj2.Object, "subsets")

	return reflect.DeepEqual(subsets1, subsets2)
}
