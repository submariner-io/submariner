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
	"github.com/submariner-io/admiral/pkg/stringset"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/util"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers/iptables"
	"github.com/submariner-io/submariner/pkg/ipam"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

func NewClusterGlobalEgressIPController(config syncer.ResourceSyncerConfig, localSubnets stringset.Interface,
	pool *ipam.IPPool) (Interface, error) {
	var err error

	klog.Info("Creating ClusterGlobalEgressIP controller")

	controller := &clusterGlobalEgressIPController{
		baseIPAllocationController: newBaseIPAllocationController(pool),
	}

	federator := federate.NewUpdateFederator(config.SourceClient, config.RestMapper, corev1.NamespaceAll)

	defaultEgressIP := &submarinerv1.ClusterGlobalEgressIP{
		ObjectMeta: metav1.ObjectMeta{
			Name: ClusterGlobalEgressIPName,
		},
	}

	defaultEgressIPObj, gvr, err := util.ToUnstructuredResource(defaultEgressIP, config.RestMapper)
	if err != nil {
		return nil, err
	}

	iptIface, err := iptables.New()
	if err != nil {
		return nil, errors.WithMessage(err, "error creating the IPTablesInterface handler")
	}

	controller.iptIface = iptIface
	controller.localSubnets = localSubnets
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
		allocatedIPs, ok, _ := unstructured.NestedStringSlice(obj.Object, "status", "allocatedIPs")
		if ok {
			if err := controller.reserveAllocatedIPsAndSyncRules(defaultEgressIP.Name, allocatedIPs); err != nil {
				klog.Errorf("Error reserving allocated GlobalIPs for %q: %v", defaultEgressIP.Name, err)
			}
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

	klog.Infof("Processing %sd for %q, Spec.NumberOfIPs: %d, Status: %#v", op, clusterGlobalEgressIP.Name,
		numberOfIPs, clusterGlobalEgressIP.Status)

	key, _ := cache.MetaNamespaceKeyFunc(clusterGlobalEgressIP)

	switch op {
	case syncer.Create, syncer.Update:
		prevStatus := clusterGlobalEgressIP.Status

		if err := c.validate(numberOfIPs, clusterGlobalEgressIP); err != nil {
			klog.Warningf("Error: %v", err)
			return checkStatusChanged(&prevStatus, &clusterGlobalEgressIP.Status, clusterGlobalEgressIP), false
		}

		requeue := c.OnCreateOrUpdate(key, numberOfIPs, &clusterGlobalEgressIP.Status)

		return checkStatusChanged(&prevStatus, &clusterGlobalEgressIP.Status, clusterGlobalEgressIP), requeue
	case syncer.Delete:
		return nil, c.onRemove(key, clusterGlobalEgressIP)
	}

	return nil, false
}

func (c *clusterGlobalEgressIPController) validate(numberOfIPs int, egressIP *submarinerv1.ClusterGlobalEgressIP) error {
	if egressIP.Name != ClusterGlobalEgressIPName {
		tryAppendStatusCondition(&egressIP.Status.Conditions, &metav1.Condition{
			Type:   string(submarinerv1.GlobalEgressIPAllocated),
			Status: metav1.ConditionFalse,
			Reason: "InvalidInstance",
			Message: fmt.Sprintf("Only the ClusterGlobalEgressIP instance with the well-known name %q is supported",
				ClusterGlobalEgressIPName),
		})

		return errors.Errorf("ClusterGlobalEgressIP with name %q is not supported, only well-known"+
			" name %q is supported", egressIP.Name, ClusterGlobalEgressIPName)
	}

	if numberOfIPs < 0 {
		tryAppendStatusCondition(&egressIP.Status.Conditions, &metav1.Condition{
			Type:    string(submarinerv1.GlobalEgressIPAllocated),
			Status:  metav1.ConditionFalse,
			Reason:  "InvalidInput",
			Message: "The NumberOfIPs cannot be negative",
		})

		return errors.Errorf("NumberOfIPs %q in %q cannot be less than 0", numberOfIPs, egressIP.Name)
	}

	if numberOfIPs == 0 {
		tryAppendStatusCondition(&egressIP.Status.Conditions, &metav1.Condition{
			Type:    string(submarinerv1.GlobalEgressIPAllocated),
			Status:  metav1.ConditionFalse,
			Reason:  "ZeroInput",
			Message: "No global IPs to allocate",
		})

		return errors.Errorf("NumberOfIPs %q in %q cannot be 0", numberOfIPs, egressIP.Name)
	}

	return nil
}

func (c *clusterGlobalEgressIPController) reserveAllocatedIPsAndSyncRules(key string, allocatedIPs []string) error {
	if len(allocatedIPs) > 0 {
		klog.V(log.DEBUG).Infof("Reserving allocatedIPs %v for %q", allocatedIPs, key)

		err := c.pool.Reserve(allocatedIPs...)
		if err != nil {
			// TODO: null out the allocatedIPs and update the status.Conditions
			return fmt.Errorf("error allocating IPs %v for %q: %v", allocatedIPs, key, err)
		}

		if err := c.programClusterGlobalEgressRules(allocatedIPs); err != nil {
			_ = c.pool.Release(allocatedIPs...)
			// TODO: null out the allocatedIPs and update the status.Conditions
			return fmt.Errorf("error syncing the IPTable rules on the node for %q: %v", key, err)
		}
	}

	return nil
}

func (c *clusterGlobalEgressIPController) OnCreateOrUpdate(key string, numberOfIPs int, status *submarinerv1.GlobalEgressIPStatus) bool {
	if numberOfIPs == len(status.AllocatedIPs) {
		klog.V(log.DEBUG).Infof("Update called for %q, but numberOfIPs %q are already allocated", key, numberOfIPs)
		return false
	}

	// If numGlobalIPs is modified, delete the existing allocation.
	if len(status.AllocatedIPs) > 0 {
		c.flushClusterGlobalEgressRules(status)

		if err := c.pool.Release(status.AllocatedIPs...); err != nil {
			klog.Errorf("Error while releasing the global IPs: %v", err)
		}
	}

	return c.allocateGlobalIPs(key, numberOfIPs, status)
}

func (c *clusterGlobalEgressIPController) onRemove(key string, egressIP *submarinerv1.ClusterGlobalEgressIP) bool { // nolint unparam
	if len(egressIP.Status.AllocatedIPs) > 0 {
		c.flushClusterGlobalEgressRules(&egressIP.Status)

		if err := c.pool.Release(egressIP.Status.AllocatedIPs...); err != nil {
			klog.Errorf("Error while releasing the global IPs: %v", err)
		}
	}

	return false
}

func (c *clusterGlobalEgressIPController) flushClusterGlobalEgressRules(status *submarinerv1.GlobalEgressIPStatus) {
	c.deleteClusterGlobalEgressRules(c.localSubnets.Elements(), c.getTargetSNATIPaddress(status.AllocatedIPs))
}

func (c *clusterGlobalEgressIPController) deleteClusterGlobalEgressRules(srcIPList []string, snatIP string) {
	for _, srcIP := range srcIPList {
		if err := c.iptIface.RemoveClusterEgressRules(srcIP, snatIP, globalNetIPTableMark); err != nil {
			klog.Errorf("Error while cleaning up ClusterEgressIPs: %v", err)
		}
	}
}

func (c *clusterGlobalEgressIPController) programClusterGlobalEgressRules(allocatedIPs []string) error {
	snatIP := c.getTargetSNATIPaddress(allocatedIPs)
	egressRulesProgrammed := []string{}

	for _, srcIP := range c.localSubnets.Elements() {
		if err := c.iptIface.AddClusterEgressRules(srcIP, snatIP, globalNetIPTableMark); err != nil {
			c.deleteClusterGlobalEgressRules(egressRulesProgrammed, snatIP)

			return err
		}

		egressRulesProgrammed = append(egressRulesProgrammed, srcIP)
	}

	return nil
}

func (c *clusterGlobalEgressIPController) getTargetSNATIPaddress(allocIPs []string) string {
	var snatIP string

	allocatedIPs := len(allocIPs)

	if allocatedIPs == 1 {
		snatIP = allocIPs[0]
	} else {
		snatIP = fmt.Sprintf("%s-%s", allocIPs[0], allocIPs[len(allocIPs)-1])
	}

	return snatIP
}

func (c *clusterGlobalEgressIPController) allocateGlobalIPs(key string, numberOfIPs int, status *submarinerv1.GlobalEgressIPStatus) bool {
	klog.Infof("Allocating %d global IP(s) for %q", numberOfIPs, key)

	status.AllocatedIPs = make([]string, 0, numberOfIPs)

	var err error
	status.AllocatedIPs, err = c.pool.Allocate(numberOfIPs)
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

	err = c.programClusterGlobalEgressRules(status.AllocatedIPs)
	if err != nil {
		_ = c.pool.Release(status.AllocatedIPs...)
		status.AllocatedIPs = []string{}

		return true
	}

	tryAppendStatusCondition(&status.Conditions, &metav1.Condition{
		Type:    string(submarinerv1.GlobalEgressIPAllocated),
		Status:  metav1.ConditionTrue,
		Reason:  "Success",
		Message: fmt.Sprintf("Allocated %d global IP(s)", numberOfIPs),
	})

	return false
}
