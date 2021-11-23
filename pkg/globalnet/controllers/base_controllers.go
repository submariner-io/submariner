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
	"github.com/submariner-io/admiral/pkg/util"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	iptiface "github.com/submariner-io/submariner/pkg/globalnet/controllers/iptables"
	"github.com/submariner-io/submariner/pkg/ipam"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

const maxRequeues = 20

func newBaseController() *baseController {
	return &baseController{
		stopCh: make(chan struct{}),
	}
}

func (c *baseController) Stop() {
	close(c.stopCh)
}

func newBaseSyncerController() *baseSyncerController {
	return &baseSyncerController{
		baseController: newBaseController(),
	}
}

func newBaseIPAllocationController(pool *ipam.IPPool, iptIface iptiface.Interface) *baseIPAllocationController {
	return &baseIPAllocationController{
		baseSyncerController: newBaseSyncerController(),
		pool:                 pool,
		iptIface:             iptIface,
	}
}

func (c *baseSyncerController) Start() error {
	return c.resourceSyncer.Start(c.stopCh) // nolint:wrapcheck  // Let the caller wrap it
}

func (c *baseSyncerController) reconcile(client dynamic.ResourceInterface, transform func(obj *unstructured.Unstructured) runtime.Object) {
	c.resourceSyncer.Reconcile(func() []runtime.Object {
		objList, err := client.List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			klog.Errorf("Error listing resources for reconciliation: %v", err)
			return nil
		}

		retList := make([]runtime.Object, 0, len(objList.Items))

		for i := range objList.Items {
			obj := transform(&objList.Items[i])
			if obj != nil {
				retList = append(retList, obj)
			}
		}

		return retList
	})
}

func (c *baseIPAllocationController) reserveAllocatedIPs(federator federate.Federator, obj *unstructured.Unstructured,
	postReserve func(allocatedIPs []string) error) error {
	var reservedIPs []string

	clearAllocatedIPs := func() {}

	ips, ok, _ := unstructured.NestedStringSlice(obj.Object, "status", "allocatedIPs")
	if ok {
		reservedIPs = ips
		clearAllocatedIPs = func() {
			_ = unstructured.SetNestedStringSlice(obj.Object, []string{}, "status", "allocatedIPs")
		}
	} else {
		ip, ok, _ := unstructured.NestedString(obj.Object, "status", "allocatedIP")
		if ok && ip != "" {
			reservedIPs = []string{ip}
			clearAllocatedIPs = func() {
				_ = unstructured.SetNestedField(obj.Object, "", "status", "allocatedIP")
			}
		}
	}

	err := c.pool.Reserve(reservedIPs...)

	if err == nil && len(reservedIPs) > 0 {
		err = postReserve(reservedIPs)
		if err != nil {
			_ = c.pool.Release(reservedIPs...)
		}
	}

	if err != nil {
		key, _ := cache.MetaNamespaceKeyFunc(obj)

		klog.Warningf("Could not reserve allocated GlobalIPs for %q: %v", key, err)

		clearAllocatedIPs()

		conditions := util.ConditionsFromUnstructured(obj, "status", "conditions")

		conditions = util.TryAppendCondition(conditions, &metav1.Condition{
			Type:    string(submarinerv1.GlobalEgressIPAllocated),
			Status:  metav1.ConditionFalse,
			Reason:  "ReserveAllocatedIPsFailed",
			Message: fmt.Sprintf("Error reserving the allocated global IP(s) from the pool: %v", err),
		})

		util.ConditionsToUnstructured(conditions, obj, "status", "conditions")

		klog.Infof("Updating %q: %#v", key, obj)

		return federator.Distribute(obj) // nolint:wrapcheck  // Let the caller wrap it
	}

	return nil
}

func (c *baseIPAllocationController) flushRulesAndReleaseIPs(key string, numRequeues int, flushRules func(allocatedIPs []string) error,
	allocatedIPs ...string) bool {
	if len(allocatedIPs) == 0 {
		return false
	}

	klog.Infof("Releasing previously allocated IPs %v for %q", allocatedIPs, key)

	err := flushRules(allocatedIPs)
	if err != nil {
		klog.Errorf("Error flushing the IP table rules for %q: %v", key, err)

		if shouldRequeue(numRequeues) {
			return true
		}
	}

	if err := c.pool.Release(allocatedIPs...); err != nil {
		klog.Errorf("Error while releasing the global IPs for %q: %v", key, err)
	}

	return false
}

func shouldRequeue(numRequeues int) bool {
	return numRequeues < maxRequeues
}

func getTargetSNATIPaddress(allocIPs []string) string {
	var snatIP string

	allocatedIPs := len(allocIPs)

	if allocatedIPs == 1 {
		snatIP = allocIPs[0]
	} else {
		snatIP = fmt.Sprintf("%s-%s", allocIPs[0], allocIPs[len(allocIPs)-1])
	}

	return snatIP
}

func checkStatusChanged(oldStatus, newStatus interface{}, retObj runtime.Object) runtime.Object {
	if equality.Semantic.DeepEqual(oldStatus, newStatus) {
		return nil
	}

	klog.Infof("Updated: %#v", newStatus)

	return retObj
}
