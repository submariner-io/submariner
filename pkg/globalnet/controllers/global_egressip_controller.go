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
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/ipam"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

func NewGlobalEgressIPController(config syncer.ResourceSyncerConfig, pool *ipam.IPPool) (Interface, error) {
	var err error

	klog.Info("Creating GlobalEgressIP controller")

	controller := &globalEgressIPController{
		baseIPAllocationController: newBaseIPAllocationController(pool),
		podWatchers:                map[string]*podWatcher{},
		watcherConfig: watcher.Config{
			RestMapper: config.RestMapper,
			Client:     config.SourceClient,
			Scheme:     config.Scheme,
		},
	}

	_, gvr, err := util.ToUnstructuredResource(&submarinerv1.GlobalEgressIP{}, config.RestMapper)
	if err != nil {
		return nil, err
	}

	client := config.SourceClient.Resource(*gvr).Namespace(corev1.NamespaceAll)
	list, err := client.List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	federator := federate.NewUpdateFederator(config.SourceClient, config.RestMapper, corev1.NamespaceAll)

	for i := range list.Items {
		err = controller.reserveAllocatedIPs(federator, &list.Items[i], func(reservedIPs []string) error {
			return nil // TODO
		})

		if err != nil {
			return nil, err
		}
	}

	controller.resourceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:                "GlobalEgressIP syncer",
		ResourceType:        &submarinerv1.GlobalEgressIP{},
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

	return controller, nil
}

func (c *globalEgressIPController) Stop() {
	c.baseController.Stop()

	c.Lock()
	defer c.Unlock()

	for _, podWatcher := range c.podWatchers {
		close(podWatcher.stopCh)
	}
}

func (c *globalEgressIPController) process(from runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	globalEgressIP := from.(*submarinerv1.GlobalEgressIP)

	klog.Infof("Processing %sd %#v", op, globalEgressIP)

	switch op {
	case syncer.Create:
		prevStatus := globalEgressIP.Status
		requeue := c.onCreate(globalEgressIP)

		return checkStatusChanged(&prevStatus, &globalEgressIP.Status, globalEgressIP), requeue
	case syncer.Update:
		// TODO handle update
	case syncer.Delete:
		return nil, c.onRemove(globalEgressIP)
	}

	return nil, false
}

func (c *globalEgressIPController) onCreate(globalEgressIP *submarinerv1.GlobalEgressIP) bool {
	key, _ := cache.MetaNamespaceKeyFunc(globalEgressIP)

	requeue := allocateIPs(key, globalEgressIP.Spec.NumberOfIPs, c.pool, &globalEgressIP.Status)
	if requeue {
		return requeue
	}

	c.Lock()
	defer c.Unlock()

	_, found := c.podWatchers[key]
	if found {
		return false
	}

	podWatcher, err := startPodWatcher(key, globalEgressIP.Namespace, c.watcherConfig)

	if err != nil {
		klog.Errorf("Error stating pod watcher for %q: %v", key, err)
		return true
	}

	c.podWatchers[key] = podWatcher

	klog.Infof("Started pod watcher for %q", key)

	return false
}

func (c *globalEgressIPController) onRemove(globalEgressIP *submarinerv1.GlobalEgressIP) bool { // nolint unparam
	key, _ := cache.MetaNamespaceKeyFunc(globalEgressIP)

	c.Lock()
	defer c.Unlock()

	podWatcher, found := c.podWatchers[key]
	if found {
		close(podWatcher.stopCh)
		delete(c.podWatchers, key)
	}

	// TODO - remove IP table rules for the allocated IPs

	return false
}

func allocateIPs(key string, numberOfIPs *int, pool *ipam.IPPool, status *submarinerv1.GlobalEgressIPStatus) bool {
	if numberOfIPs == nil {
		one := 1
		numberOfIPs = &one
	}

	if *numberOfIPs < 0 {
		tryAppendStatusCondition(&status.Conditions, &metav1.Condition{
			Type:    string(submarinerv1.GlobalEgressIPAllocated),
			Status:  metav1.ConditionFalse,
			Reason:  "InvalidInput",
			Message: "The NumberOfIPs cannot be negative",
		})

		return false
	}

	if *numberOfIPs == 0 {
		tryAppendStatusCondition(&status.Conditions, &metav1.Condition{
			Type:    string(submarinerv1.GlobalEgressIPAllocated),
			Status:  metav1.ConditionFalse,
			Reason:  "ZeroInput",
			Message: "No global IPs to allocate",
		})
	}

	if *numberOfIPs == len(status.AllocatedIPs) {
		return false
	}

	klog.Infof("Allocating %d global IP(s) for %q", *numberOfIPs, key)

	var err error

	status.AllocatedIPs, err = pool.Allocate(*numberOfIPs)
	if err != nil {
		klog.Errorf("Error allocating IPs for %q: %v", key, err)
		tryAppendStatusCondition(&status.Conditions, &metav1.Condition{
			Type:    string(submarinerv1.GlobalEgressIPAllocated),
			Status:  metav1.ConditionFalse,
			Reason:  "IPPoolAllocationFailed",
			Message: fmt.Sprintf("Error allocating %d global IP(s) from the pool: %v", numberOfIPs, err),
		})

		return true
	}

	// TODO - remove IP table rules for previous allocated IPs

	if *numberOfIPs == 0 {
		return false
	}

	// TODO - add IP table rules for the allocated IPs

	tryAppendStatusCondition(&status.Conditions, &metav1.Condition{
		Type:    string(submarinerv1.GlobalEgressIPAllocated),
		Status:  metav1.ConditionTrue,
		Reason:  "Success",
		Message: fmt.Sprintf("Allocated %d global IP(s)", *numberOfIPs),
	})

	return false
}

func tryAppendStatusCondition(conditions *[]metav1.Condition, newCond *metav1.Condition) {
	updatedConditions := util.TryAppendCondition(*conditions, *newCond)
	if updatedConditions == nil {
		return
	}

	*conditions = updatedConditions
}

func checkStatusChanged(oldStatus, newStatus interface{}, retObj runtime.Object) runtime.Object {
	if equality.Semantic.DeepEqual(oldStatus, newStatus) {
		return nil
	}

	klog.Infof("Updated: %#v", newStatus)

	return retObj
}
