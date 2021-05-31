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

	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/util"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers/ipam"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

func NewClusterGlobalEgressIPController(config syncer.ResourceSyncerConfig, pool *ipam.IPPool) (Interface, error) {
	var err error

	klog.Info("Creating ClusterGlobalEgressIP controller")

	defaultEgressIP := &submarinerv1.ClusterGlobalEgressIP{
		ObjectMeta: metav1.ObjectMeta{
			Name: ClusterGlobalEgressIPName,
		},
	}

	defaultEgressIPObj, gvr, err := util.ToUnstructuredResource(defaultEgressIP, config.RestMapper)
	if err != nil {
		return nil, err
	}

	client := config.SourceClient.Resource(*gvr)
	_, err = client.Get(context.TODO(), ClusterGlobalEgressIPName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = client.Create(context.TODO(), defaultEgressIPObj, metav1.CreateOptions{})
	}

	if err != nil {
		return nil, err
	}

	// TODO - reserve the allocated IPs in the IPPool cache.

	controller := &clusterGlobalEgressIPController{
		baseController: newBaseController(),
		pool:           pool,
	}

	controller.resourceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:                "ClusterGlobalEgressIP syncer",
		ResourceType:        &submarinerv1.ClusterGlobalEgressIP{},
		SourceClient:        config.SourceClient,
		SourceNamespace:     corev1.NamespaceAll,
		RestMapper:          config.RestMapper,
		Federator:           federate.NewUpdateStatusFederator(config.SourceClient, config.RestMapper, corev1.NamespaceAll),
		Scheme:              config.Scheme,
		Transform:           controller.process,
		ResourcesEquivalent: syncer.AreSpecsEquivalent,
	})

	if err != nil {
		return nil, err
	}

	return controller, nil
}

func (c *clusterGlobalEgressIPController) Start() error {
	klog.Info("Starting ClusterGlobalEgressIP controller")

	return c.resourceSyncer.Start(c.stopCh)
}

func (c *clusterGlobalEgressIPController) process(from runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	clusterGlobalEgressIP := from.(*submarinerv1.ClusterGlobalEgressIP)

	klog.Infof("Processing %sd %#v", op, clusterGlobalEgressIP)

	switch op {
	case syncer.Create:
		prevStatus := clusterGlobalEgressIP.Status
		requeue := c.onCreate(clusterGlobalEgressIP)

		return checkGlobalEgressIPStatusChanged(&prevStatus, &clusterGlobalEgressIP.Status, clusterGlobalEgressIP), requeue
	case syncer.Update:
		// TODO handle update
	case syncer.Delete:
		return nil, c.onRemove(clusterGlobalEgressIP)
	}

	return nil, false
}

func (c *clusterGlobalEgressIPController) onCreate(egressIP *submarinerv1.ClusterGlobalEgressIP) bool {
	key, _ := cache.MetaNamespaceKeyFunc(egressIP)

	if egressIP.Name != ClusterGlobalEgressIPName {
		tryAppendStatusCondition(&egressIP.Status, &metav1.Condition{
			Type:   string(submarinerv1.GlobalEgressIPAllocated),
			Status: metav1.ConditionFalse,
			Reason: "InvalidInstance",
			Message: fmt.Sprintf("Only the ClusterGlobalEgressIP instance with the well-known name %q is supported",
				ClusterGlobalEgressIPName),
		})

		return false
	}

	return allocateIPs(key, egressIP.Spec.NumberOfIPs, c.pool, &egressIP.Status)
}

func (c *clusterGlobalEgressIPController) onRemove(egressIP *submarinerv1.ClusterGlobalEgressIP) bool { // nolint unparam
	// TODO - remove IP table rules for the allocated IPs

	return false
}
