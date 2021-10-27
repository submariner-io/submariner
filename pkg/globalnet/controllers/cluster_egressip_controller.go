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
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers/iptables"
	"github.com/submariner-io/submariner/pkg/ipam"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

func NewClusterGlobalEgressIPController(config *syncer.ResourceSyncerConfig, localSubnets []string,
	pool *ipam.IPPool) (Interface, error) {
	// We'll panic if config is nil, this is intentional
	var err error

	klog.Info("Creating ClusterGlobalEgressIP controller")

	iptIface, err := iptables.New()
	if err != nil {
		return nil, errors.WithMessage(err, "error creating the IPTablesInterface handler")
	}

	controller := &clusterGlobalEgressIPController{
		baseIPAllocationController: newBaseIPAllocationController(pool, iptIface),
		localSubnets:               localSubnets,
	}

	federator := federate.NewUpdateFederator(config.SourceClient, config.RestMapper, corev1.NamespaceAll)

	numberOfIPs := DefaultNumberOfClusterEgressIPs
	defaultEgressIP := &submarinerv1.ClusterGlobalEgressIP{
		ObjectMeta: metav1.ObjectMeta{
			Name: constants.ClusterGlobalEgressIPName,
		},
		Spec: submarinerv1.ClusterGlobalEgressIPSpec{
			NumberOfIPs: &numberOfIPs,
		},
	}

	defaultEgressIPObj, gvr, err := util.ToUnstructuredResource(defaultEgressIP, config.RestMapper)
	if err != nil {
		return nil, err
	}

	client := config.SourceClient.Resource(*gvr)
	obj, err := client.Get(context.TODO(), defaultEgressIP.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.Infof("Creating ClusterGlobalEgressIP resource %q", defaultEgressIP.Name)

		_, err = client.Create(context.TODO(), defaultEgressIPObj, metav1.CreateOptions{})
		if err != nil {
			return nil, errors.WithMessagef(err, "error creating ClusterGlobalEgressIP resource %q", defaultEgressIP.Name)
		}
	} else if err != nil {
		return nil, errors.WithMessagef(err, "error retrieving ClusterGlobalEgressIP resource %q", defaultEgressIP.Name)
	}

	if obj != nil {
		err := controller.reserveAllocatedIPs(federator, obj, func(reservedIPs []string) error {
			return controller.programClusterGlobalEgressRules(reservedIPs)
		})
		if err != nil {
			return nil, err
		}
	}

	controller.resourceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:                "ClusterGlobalEgressIP syncer",
		ResourceType:        &submarinerv1.ClusterGlobalEgressIP{},
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

func (c *clusterGlobalEgressIPController) process(from runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	clusterGlobalEgressIP := from.(*submarinerv1.ClusterGlobalEgressIP)

	numberOfIPs := 1
	if clusterGlobalEgressIP.Spec.NumberOfIPs != nil {
		numberOfIPs = *clusterGlobalEgressIP.Spec.NumberOfIPs
	}

	klog.Infof("Processing %sd ClusterGlobalEgressIP %q, Spec.NumberOfIPs: %d, Status: %#v", op, clusterGlobalEgressIP.Name,
		numberOfIPs, clusterGlobalEgressIP.Status)

	key, _ := cache.MetaNamespaceKeyFunc(clusterGlobalEgressIP)

	switch op {
	case syncer.Create, syncer.Update:
		prevStatus := clusterGlobalEgressIP.Status

		if !c.validate(numberOfIPs, clusterGlobalEgressIP) {
			return checkStatusChanged(&prevStatus, &clusterGlobalEgressIP.Status, clusterGlobalEgressIP), false
		}

		requeue := c.onCreateOrUpdate(key, numberOfIPs, &clusterGlobalEgressIP.Status, numRequeues)

		return checkStatusChanged(&prevStatus, &clusterGlobalEgressIP.Status, clusterGlobalEgressIP), requeue
	case syncer.Delete:
		return nil, c.onDelete(key, &clusterGlobalEgressIP.Status, numRequeues)
	}

	return nil, false
}

func (c *clusterGlobalEgressIPController) validate(numberOfIPs int, egressIP *submarinerv1.ClusterGlobalEgressIP) bool {
	if egressIP.Name != constants.ClusterGlobalEgressIPName {
		egressIP.Status.Conditions = util.TryAppendCondition(egressIP.Status.Conditions, &metav1.Condition{
			Type:   string(submarinerv1.GlobalEgressIPAllocated),
			Status: metav1.ConditionFalse,
			Reason: "InvalidInstance",
			Message: fmt.Sprintf("Only the ClusterGlobalEgressIP instance with the well-known name %q is supported",
				constants.ClusterGlobalEgressIPName),
		})

		return false
	}

	if numberOfIPs < 0 {
		egressIP.Status.Conditions = util.TryAppendCondition(egressIP.Status.Conditions, &metav1.Condition{
			Type:    string(submarinerv1.GlobalEgressIPAllocated),
			Status:  metav1.ConditionFalse,
			Reason:  "InvalidInput",
			Message: "The NumberOfIPs cannot be negative",
		})

		return false
	}

	if numberOfIPs == 0 {
		egressIP.Status.Conditions = util.TryAppendCondition(egressIP.Status.Conditions, &metav1.Condition{
			Type:    string(submarinerv1.GlobalEgressIPAllocated),
			Status:  metav1.ConditionFalse,
			Reason:  "ZeroInput",
			Message: "The specified NumberOfIPs is 0",
		})
	}

	return true
}

func (c *clusterGlobalEgressIPController) onCreateOrUpdate(key string, numberOfIPs int, status *submarinerv1.GlobalEgressIPStatus,
	numRequeues int) bool {
	if numberOfIPs == len(status.AllocatedIPs) {
		klog.V(log.DEBUG).Infof("Update called for %q, but numberOfIPs %d are already allocated", key, numberOfIPs)
		return false
	}

	if requeue := c.flushRulesAndReleaseIPs(key, numRequeues, c.flushClusterGlobalEgressRules, status.AllocatedIPs...); requeue {
		return true
	}

	return c.allocateGlobalIPs(key, numberOfIPs, status)
}

func (c *clusterGlobalEgressIPController) onDelete(key string, status *submarinerv1.GlobalEgressIPStatus, numRequeues int) bool {
	return c.flushRulesAndReleaseIPs(key, numRequeues, c.flushClusterGlobalEgressRules, status.AllocatedIPs...)
}

func (c *clusterGlobalEgressIPController) flushClusterGlobalEgressRules(allocatedIPs []string) error {
	return c.deleteClusterGlobalEgressRules(c.localSubnets, getTargetSNATIPaddress(allocatedIPs))
}

func (c *clusterGlobalEgressIPController) deleteClusterGlobalEgressRules(srcIPList []string, snatIP string) error {
	for _, srcIP := range srcIPList {
		if err := c.iptIface.RemoveClusterEgressRules(srcIP, snatIP, globalNetIPTableMark); err != nil {
			return err
		}
	}

	return nil
}

func (c *clusterGlobalEgressIPController) programClusterGlobalEgressRules(allocatedIPs []string) error {
	snatIP := getTargetSNATIPaddress(allocatedIPs)
	egressRulesProgrammed := []string{}

	for _, srcIP := range c.localSubnets {
		if err := c.iptIface.AddClusterEgressRules(srcIP, snatIP, globalNetIPTableMark); err != nil {
			_ = c.deleteClusterGlobalEgressRules(egressRulesProgrammed, snatIP)

			return err
		}

		egressRulesProgrammed = append(egressRulesProgrammed, srcIP)
	}

	return nil
}

func (c *clusterGlobalEgressIPController) allocateGlobalIPs(key string, numberOfIPs int, status *submarinerv1.GlobalEgressIPStatus) bool {
	klog.Infof("Allocating %d global IP(s) for %q", numberOfIPs, key)

	status.AllocatedIPs = nil

	if numberOfIPs == 0 {
		return false
	}

	allocatedIPs, err := c.pool.Allocate(numberOfIPs)
	if err != nil {
		klog.Errorf("Error allocating IPs for %q: %v", key, err)

		status.Conditions = util.TryAppendCondition(status.Conditions, &metav1.Condition{
			Type:    string(submarinerv1.GlobalEgressIPAllocated),
			Status:  metav1.ConditionFalse,
			Reason:  "IPPoolAllocationFailed",
			Message: fmt.Sprintf("Error allocating %d global IP(s) from the pool: %v", numberOfIPs, err),
		})

		return true
	}

	err = c.programClusterGlobalEgressRules(allocatedIPs)
	if err != nil {
		klog.Errorf("Error programming egress IP table rules for %q: %v", key, err)

		status.Conditions = util.TryAppendCondition(status.Conditions, &metav1.Condition{
			Type:    string(submarinerv1.GlobalEgressIPAllocated),
			Status:  metav1.ConditionFalse,
			Reason:  "ProgramIPTableRulesFailed",
			Message: fmt.Sprintf("Error programming egress rules: %v", err),
		})

		_ = c.pool.Release(allocatedIPs...)

		return true
	}

	status.Conditions = util.TryAppendCondition(status.Conditions, &metav1.Condition{
		Type:    string(submarinerv1.GlobalEgressIPAllocated),
		Status:  metav1.ConditionTrue,
		Reason:  "Success",
		Message: fmt.Sprintf("Allocated %d global IP(s)", numberOfIPs),
	})

	status.AllocatedIPs = allocatedIPs

	return false
}
