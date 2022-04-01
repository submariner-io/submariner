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
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/util"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/metrics"
	"github.com/submariner-io/submariner/pkg/ipam"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

func NewGlobalEgressIPController(config *syncer.ResourceSyncerConfig, pool *ipam.IPPool) (Interface, error) {
	// We'll panic if config is nil, this is intentional
	var err error

	klog.Info("Creating GlobalEgressIP controller")

	controller := &globalEgressIPController{
		baseIPAllocationController: newBaseIPAllocationController(pool),
		podWatchers:                map[string]*egressPodWatcher{},
		watcherConfig: watcher.Config{
			RestMapper: config.RestMapper,
			Client:     config.SourceClient,
			Scheme:     config.Scheme,
		},
	}

	_, gvr, err := util.ToUnstructuredResource(&submarinerv1.GlobalEgressIP{}, config.RestMapper)
	if err != nil {
		return nil, errors.Wrap(err, "error converting resource")
	}

	client := config.SourceClient.Resource(*gvr).Namespace(corev1.NamespaceAll)

	list, err := client.List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "error listing the resources")
	}

	federator := federate.NewUpdateFederator(config.SourceClient, config.RestMapper, corev1.NamespaceAll)

	for i := range list.Items {
		err = controller.reserveAllocatedIPs(federator, &list.Items[i], func(reservedIPs []string) error {
			metrics.RecordAllocateGlobalEgressIPs(pool.GetCIDR(), len(reservedIPs))
			specObj := util.GetSpec(&list.Items[i])
			spec := &submarinerv1.GlobalEgressIPSpec{}
			_ = runtime.DefaultUnstructuredConverter.FromUnstructured(specObj.(map[string]interface{}), spec)
			return nil
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
		return nil, errors.Wrap(err, "error creating the syncer")
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

	numberOfIPs := 1
	if globalEgressIP.Spec.NumberOfIPs != nil {
		numberOfIPs = *globalEgressIP.Spec.NumberOfIPs
	}

	key, _ := cache.MetaNamespaceKeyFunc(globalEgressIP)

	klog.Infof("Processing %sd GlobalEgressIP %q, NumberOfIPs: %d, PodSelector: %#v, Status: %#v", op, key,
		numberOfIPs, globalEgressIP.Spec.PodSelector, globalEgressIP.Status)

	switch op {
	case syncer.Create, syncer.Update:
		prevStatus := globalEgressIP.Status

		requeue := false
		if c.validate(numberOfIPs, globalEgressIP) {
			requeue = c.onCreateOrUpdate(key, numberOfIPs, globalEgressIP)
		}

		return checkStatusChanged(&prevStatus, &globalEgressIP.Status, globalEgressIP), requeue
	case syncer.Delete:
		c.onDelete(globalEgressIP)
		return nil, false
	}

	return nil, false
}

func (c *globalEgressIPController) onCreateOrUpdate(key string, numberOfIPs int, globalEgressIP *submarinerv1.GlobalEgressIP) bool {
	if numberOfIPs == len(globalEgressIP.Status.AllocatedIPs) {
		klog.V(log.DEBUG).Infof("Update called for %q, but numberOfIPs %d are already allocated", key, numberOfIPs)
		return false
	}

	c.releaseIPs(key, globalEgressIP.Status.AllocatedIPs...)

	// All we need to do now in globalnet is allocate the IPs
	return c.allocateGlobalIPs(key, numberOfIPs, globalEgressIP) ||
		!c.createPodWatcher(key, numberOfIPs, globalEgressIP)
}

func (c *globalEgressIPController) allocateGlobalIPs(key string, numberOfIPs int,
	globalEgressIP *submarinerv1.GlobalEgressIP,
) bool {
	klog.Infof("Allocating %d global IP(s) for %q", numberOfIPs, key)

	if numberOfIPs == 0 {
		globalEgressIP.Status.AllocatedIPs = nil
		globalEgressIP.Status.Conditions = util.TryAppendCondition(globalEgressIP.Status.Conditions, &metav1.Condition{
			Type:    string(submarinerv1.GlobalEgressIPAllocated),
			Status:  metav1.ConditionFalse,
			Reason:  "ZeroInput",
			Message: "The specified NumberOfIPs is 0",
		})

		return false
	}

	if numberOfIPs == len(globalEgressIP.Status.AllocatedIPs) {
		return false
	}

	globalEgressIP.Status.AllocatedIPs = nil

	allocatedIPs, err := c.pool.Allocate(numberOfIPs)
	if err != nil {
		klog.Errorf("Error allocating IPs for %q: %v", key, err)

		globalEgressIP.Status.Conditions = util.TryAppendCondition(globalEgressIP.Status.Conditions, &metav1.Condition{
			Type:    string(submarinerv1.GlobalEgressIPAllocated),
			Status:  metav1.ConditionFalse,
			Reason:  "IPPoolAllocationFailed",
			Message: fmt.Sprintf("Error allocating %d global IP(s) from the pool: %v", numberOfIPs, err),
		})

		return true
	}

	metrics.RecordAllocateGlobalEgressIPs(c.pool.GetCIDR(), numberOfIPs)

	globalEgressIP.Status.Conditions = util.TryAppendCondition(globalEgressIP.Status.Conditions, &metav1.Condition{
		Type:    string(submarinerv1.GlobalEgressIPAllocated),
		Status:  metav1.ConditionFalse,
		Reason:  "NoDatapathRules",
		Message: fmt.Sprintf("Allocated %d global IP(s)", numberOfIPs),
	})

	globalEgressIP.Status.AllocatedIPs = allocatedIPs

	klog.Infof("Allocated %v global IP(s) for %q", globalEgressIP.Status.AllocatedIPs, key)

	return false
}

func (c *globalEgressIPController) validate(numberOfIPs int, egressIP *submarinerv1.GlobalEgressIP) bool {
	if numberOfIPs < 0 {
		egressIP.Status.Conditions = util.TryAppendCondition(egressIP.Status.Conditions, &metav1.Condition{
			Type:    string(submarinerv1.GlobalEgressIPAllocated),
			Status:  metav1.ConditionFalse,
			Reason:  "InvalidInput",
			Message: "The NumberOfIPs cannot be negative",
		})

		return false
	}

	return true
}

func (c *globalEgressIPController) onDelete(globalEgressIP *submarinerv1.GlobalEgressIP) {
	key, _ := cache.MetaNamespaceKeyFunc(globalEgressIP)

	c.Lock()
	defer c.Unlock()

	podWatcher, found := c.podWatchers[key]
	if found {
		close(podWatcher.stopCh)
		delete(c.podWatchers, key)
	}

	metrics.RecordDeallocateGlobalEgressIPs(c.pool.GetCIDR(), len(globalEgressIP.Status.AllocatedIPs))
	c.releaseIPs(key, globalEgressIP.Status.AllocatedIPs...)

	klog.Infof("Successfully released Allocated IPs %v for %q ", globalEgressIP.Status.AllocatedIPs, key)
}

func (c *globalEgressIPController) createPodWatcher(key string, numberOfIPs int,
	globalEgressIP *submarinerv1.GlobalEgressIP,
) bool {
	c.Lock()
	defer c.Unlock()

	prevPodWatcher, found := c.podWatchers[key]
	if found {
		if !equality.Semantic.DeepEqual(prevPodWatcher.podSelector, globalEgressIP.Spec.PodSelector) {
			klog.Errorf("PodSelector for %q cannot be updated after creation", key)

			globalEgressIP.Status.Conditions = util.TryAppendCondition(globalEgressIP.Status.Conditions, &metav1.Condition{
				Type:    string(submarinerv1.GlobalEgressIPUpdated),
				Status:  metav1.ConditionFalse,
				Reason:  "PodSelectorUpdateNotSupported",
				Message: "The PodSelector cannot be updated after creation",
			})
		}

		return true
	}

	if numberOfIPs == 0 {
		return true
	}

	podWatcher, err := startEgressPodWatcher(key, globalEgressIP.Namespace, &c.watcherConfig, globalEgressIP.Spec.PodSelector)
	if err != nil {
		klog.Errorf("Error starting pod watcher for %q: %v", key, err)
		return false
	}

	c.podWatchers[key] = podWatcher
	podWatcher.podSelector = globalEgressIP.Spec.PodSelector

	klog.Infof("Started pod watcher for %q", key)

	return true
}
