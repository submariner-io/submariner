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
	"fmt"

	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/util"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers/iprules"
	"github.com/submariner-io/submariner/pkg/ipam"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
)

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

func newBaseIPAllocationController(pool *ipam.IPPool) *baseIPAllocationController {
	return &baseIPAllocationController{
		baseSyncerController: newBaseSyncerController(),
		pool:                 pool,
	}
}

func newBaseEgressIPAllocationController(pool *ipam.IPPool, ipRuleIface iprules.Interface) *baseEgressIPAllocationController {
	return &baseEgressIPAllocationController{
		baseIPAllocationController: newBaseIPAllocationController(pool),
		ipRuleIface:                ipRuleIface,
	}
}

func (c *baseSyncerController) Start() error {
	return c.resourceSyncer.Start(c.stopCh)
}

// nolint unparam - 'federator' will be used
func (c *baseIPAllocationController) reserveAllocatedIPs(federator federate.Federator, obj *unstructured.Unstructured) error {
	var err error

	ips, ok, _ := unstructured.NestedStringSlice(obj.Object, "status", "allocatedIPs")
	if ok {
		err = c.pool.Reserve(ips...)
		if err != nil {
			// TODO: null out the allocatedIPs
			return err
		}
	} else {
		ip, _, _ := unstructured.NestedString(obj.Object, "status", "allocatedIP")
		if ip != "" {
			err = c.pool.Reserve(ip)
			if err != nil {
				// TODO: null out the allocatedIP
				return err
			}
		}
	}

	// if err != nil {
	//	 TODO: call federator.Distribute to update the obj
	// }

	return nil
}

func (c *baseEgressIPAllocationController) allocateIPs(key string, numberOfIPs *int, status *submarinerv1.GlobalEgressIPStatus) bool {
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

	err := c.pool.Release(status.AllocatedIPs...)
	if err != nil {
		klog.Errorf("Error releasing allocated IPs for %q: %v", key, err)
	}

	err = c.ipRuleIface.RemoveRules(status.AllocatedIPs...)
	if err != nil {
		klog.Errorf("Error removing IP table rules for %q: %v", key, err)
	}

	if *numberOfIPs == 0 {
		return false
	}

	allocatedIPs, err := c.pool.Allocate(*numberOfIPs)
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

	err = c.ipRuleIface.AddRules(allocatedIPs...)
	if err != nil {
		klog.Errorf("Error adding IP table rules for %q: %v", key, err)

		err := c.pool.Release(status.AllocatedIPs...)
		if err != nil {
			klog.Errorf("Error releasing allocated IPs for %q: %v", key, err)
		}

		tryAppendStatusCondition(&status.Conditions, &metav1.Condition{
			Type:    string(submarinerv1.GlobalEgressIPAllocated),
			Status:  metav1.ConditionFalse,
			Reason:  "AddIPTableRulesFailed",
			Message: fmt.Sprintf("Error adding IP table rules: %v", err),
		})

		return true
	}

	status.AllocatedIPs = allocatedIPs

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

	klog.V(log.DEBUG).Infof("Updated: %#v", newStatus)

	return retObj
}
