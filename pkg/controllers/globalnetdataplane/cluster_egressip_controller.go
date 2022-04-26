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

package globalnetdataplane

import (
	"fmt"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/util"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/iptables"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

func NewClusterGlobalEgressIPController(config *syncer.ResourceSyncerConfig, localSubnets []string) (Interface, error) {
	// We'll panic if config is nil, this is intentional
	var err error

	klog.Info("Creating ClusterGlobalEgressIP dataplane controller")

	controller := &clusterGlobalEgressIPController{
		baseSyncerController: newBaseSyncerController(),
		localSubnets:         localSubnets,
	}

	iptIface, err := iptables.New()
	if err != nil {
		return nil, errors.WithMessage(err, "error creating the IPTablesInterface handler")
	}

	controller.baseController.ipt = iptIface

	federator := federate.NewUpdateFederator(config.SourceClient, config.RestMapper, corev1.NamespaceAll)

	controller.resourceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:                "ClusterGlobalDataplaneEgressIP syncer",
		ResourceType:        &submarinerv1.ClusterGlobalEgressIP{},
		SourceClient:        config.SourceClient,
		SourceNamespace:     corev1.NamespaceAll,
		RestMapper:          config.RestMapper,
		Federator:           federator,
		Scheme:              config.Scheme,
		Transform:           controller.process,
		ResourcesEquivalent: AreSpecsAndStatusEquivalent,
		// Only change daplane rules for clusterGlobalEgressIP objects that are local
		// this node where this controller is running.
		ShouldProcess: shouldProcessClusterGlobalEgressIP,
	})

	if err != nil {
		return nil, errors.Wrap(err, "error creating resource syncer")
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

		requeue := c.onCreateOrUpdate(key, numberOfIPs, &clusterGlobalEgressIP.Status, numRequeues)

		return checkStatusChanged(&prevStatus, &clusterGlobalEgressIP.Status, clusterGlobalEgressIP), requeue
	case syncer.Delete:
		return nil, c.onDelete(key, &clusterGlobalEgressIP.Status, numRequeues)
	}

	return nil, false
}

func (c *clusterGlobalEgressIPController) onCreateOrUpdate(key string, numberOfIPs int, status *submarinerv1.GlobalEgressIPStatus,
	numRequeues int,
) bool {
	if numberOfIPs != len(status.AllocatedIPs) {
		klog.V(log.DEBUG).Infof("Event received for %q, but numberOfIPs %d are not yet allocated by globalnet controller", key, numberOfIPs)
		return false
	}

	if requeue := FlushAllocatedIPRules(key, numRequeues, c.flushClusterGlobalEgressRules,
		status.AllocatedIPs...); requeue {
		return true
	}

	err := c.programClusterGlobalEgressRules(status.AllocatedIPs)
	if err != nil {
		klog.Errorf("Error programming egress IP table rules for %q: %v", key, err)

		status.Conditions = util.TryAppendCondition(status.Conditions, &metav1.Condition{
			Type:    string(submarinerv1.GlobalEgressIPAllocated),
			Status:  metav1.ConditionFalse,
			Reason:  "ProgramIPTableRulesFailed",
			Message: fmt.Sprintf("Error programming egress rules: %v", err),
		})

		return true
	}

	status.Conditions = util.TryAppendCondition(status.Conditions, &metav1.Condition{
		Type:    string(submarinerv1.GlobalEgressIPAllocated),
		Status:  metav1.ConditionTrue,
		Reason:  "DatapathRuleWriteSuccess",
		Message: fmt.Sprintf("Allocated %d global IP(s)", numberOfIPs),
	})

	return false
}

func (c *clusterGlobalEgressIPController) onDelete(key string, status *submarinerv1.GlobalEgressIPStatus, numRequeues int) bool {
	return FlushAllocatedIPRules(key, numRequeues, c.flushClusterGlobalEgressRules,
		status.AllocatedIPs...)
}

func (c *clusterGlobalEgressIPController) flushClusterGlobalEgressRules(allocatedIPs []string) error {
	return c.deleteClusterGlobalEgressRules(c.localSubnets, getTargetSNATIPaddress(allocatedIPs))
}

func (c *clusterGlobalEgressIPController) deleteClusterGlobalEgressRules(srcIPList []string, snatIP string) error {
	for _, srcIP := range srcIPList {
		if err := c.ipt.RemoveClusterEgressRules(srcIP, snatIP, globalNetIPTableMark); err != nil {
			return err // nolint:wrapcheck  // Let the caller wrap it
		}
	}

	return nil
}

func (c *clusterGlobalEgressIPController) programClusterGlobalEgressRules(allocatedIPs []string) error {
	snatIP := getTargetSNATIPaddress(allocatedIPs)
	egressRulesProgrammed := []string{}

	for _, srcIP := range c.localSubnets {
		if err := c.ipt.AddClusterEgressRules(srcIP, snatIP, globalNetIPTableMark); err != nil {
			_ = c.deleteClusterGlobalEgressRules(egressRulesProgrammed, snatIP)

			return err // nolint:wrapcheck  // Let the caller wrap it
		}

		egressRulesProgrammed = append(egressRulesProgrammed, srcIP)
	}

	return nil
}
