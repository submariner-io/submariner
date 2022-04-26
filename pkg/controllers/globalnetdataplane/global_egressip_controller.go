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
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"os"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/util"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/ipam"
	"github.com/submariner-io/submariner/pkg/ipset"
	"github.com/submariner-io/submariner/pkg/iptables"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
	utilexec "k8s.io/utils/exec"
)

func NewGlobalEgressIPController(config *syncer.ResourceSyncerConfig, pool *ipam.IPPool) (Interface, error) {
	// We'll panic if config is nil, this is intentional
	var err error

	klog.Info("Creating GlobalEgressIP dataplane controller")

	controller := &globalEgressIPController{
		podWatchers: map[string]*egressPodWatcher{},
		watcherConfig: watcher.Config{
			RestMapper: config.RestMapper,
			Client:     config.SourceClient,
			Scheme:     config.Scheme,
		},
	}

	iptIface, err := iptables.New()
	if err != nil {
		return nil, errors.WithMessage(err, "error creating the IPTablesInterface handler")
	}

	controller.ipt = iptIface

	controller.ipSetIface = ipset.New(utilexec.New())

	federator := federate.NewUpdateFederator(config.SourceClient, config.RestMapper, corev1.NamespaceAll)

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

	// We need some better early exit paths here to ensure that the allocation has already happened,
	// maybe looking in the conditions?
	numberOfIPs := 1
	if globalEgressIP.Spec.NumberOfIPs != nil {
		numberOfIPs = *globalEgressIP.Spec.NumberOfIPs
	}

	key, _ := cache.MetaNamespaceKeyFunc(globalEgressIP)

	klog.Infof("Processing %sd GlobalEgressIP %q, NumberOfIPs: %d, PodSelector: %#v, Status: %#v", op, key,
		numberOfIPs, globalEgressIP.Spec.PodSelector, globalEgressIP.Status)

	switch op {
	// (astoycos)TODO this probably only needs to be done on update, since we should be responding to Updates only from the
	// globalnet controller
	case syncer.Create, syncer.Update:
		prevStatus := globalEgressIP.Status

		requeue := false
		if c.validate(numberOfIPs, globalEgressIP) {
			requeue = c.onCreateOrUpdate(key, numberOfIPs, globalEgressIP, numRequeues)
		}

		return checkStatusChanged(&prevStatus, &globalEgressIP.Status, globalEgressIP), requeue
	case syncer.Delete:
		return nil, c.onDelete(numRequeues, globalEgressIP)
	}

	return nil, false
}

func (c *globalEgressIPController) onCreateOrUpdate(key string, numberOfIPs int, globalEgressIP *submarinerv1.GlobalEgressIP,
	numRequeues int,
) bool {
	namedIPSet := c.newNamedIPSet(key)

	requeue := false
	if numberOfIPs != len(globalEgressIP.Status.AllocatedIPs) {
		requeue = c.flushGlobalEgressRules(key, namedIPSet.Name(), numRequeues, globalEgressIP)
	}

	return requeue || c.processGlobalIPs(key, numberOfIPs, globalEgressIP, namedIPSet) ||
		!c.createPodWatcher(key, namedIPSet, numberOfIPs, globalEgressIP)
}

// nolint:wrapcheck  // No need to wrap these errors.
func (c *globalEgressIPController) programGlobalEgressRules(key string, allocatedIPs []string, podSelector *metav1.LabelSelector,
	namedIPSet ipset.Named,
) error {
	err := namedIPSet.Create(true)
	if err != nil {
		return errors.Wrapf(err, "error creating the IP set chain %q", namedIPSet.Name())
	}

	snatIP := getTargetSNATIPaddress(allocatedIPs)
	if podSelector != nil {
		if err := c.ipt.AddEgressRulesForPods(key, namedIPSet.Name(), snatIP, globalNetIPTableMark); err != nil {
			_ = c.ipt.RemoveEgressRulesForPods(key, namedIPSet.Name(), snatIP, globalNetIPTableMark)
			return err
		}
	} else {
		if err := c.ipt.AddEgressRulesForNamespace(key, namedIPSet.Name(), snatIP, globalNetIPTableMark); err != nil {
			_ = c.ipt.RemoveEgressRulesForNamespace(key, namedIPSet.Name(), snatIP, globalNetIPTableMark)
			return err
		}
	}

	return nil
}

func (c *globalEgressIPController) processGlobalIPs(key string, numberOfIPs int,
	globalEgressIP *submarinerv1.GlobalEgressIP, namedIPSet ipset.Named,
) bool {
	klog.Infof("Processing %d global IP(s) for %q", numberOfIPs, key)

	// if numberOfIPs == 0 {
	// 	globalEgressIP.Status.AllocatedIPs = nil
	// 	globalEgressIP.Status.Conditions = util.TryAppendCondition(globalEgressIP.Status.Conditions, &metav1.Condition{
	// 		Type:    string(submarinerv1.GlobalEgressIPAllocated),
	// 		Status:  metav1.ConditionFalse,
	// 		Reason:  "ZeroInput",
	// 		Message: "The specified NumberOfIPs is 0",
	// 	})

	// 	return false
	// }

	if numberOfIPs == len(globalEgressIP.Status.AllocatedIPs) {
		return false
	}

	// allocatedIPs, err := c.pool.Allocate(numberOfIPs)
	// if err != nil {
	// 	klog.Errorf("Error allocating IPs for %q: %v", key, err)

	// nolint: gocritic // TODO MAG POC
	// 	globalEgressIP.Status.Conditions = util.TryAppendCondition(globalEgressIP.Status.Conditions, &metav1.Condition{
	// 		Type:    string(submarinerv1.GlobalEgressIPAllocated),
	// 		Status:  metav1.ConditionFalse,
	// 		Reason:  "IPPoolAllocationFailed",
	// 		Message: fmt.Sprintf("Error allocating %d global IP(s) from the pool: %v", numberOfIPs, err),
	// 	})

	// 	return true
	// }

	// On failure it will be the job of the globalnetcontroller to unallocate IPs, and other GWs to remove iptables
	// rules if they see unallocation occurring
	err := c.programGlobalEgressRules(key, globalEgressIP.Status.AllocatedIPs, globalEgressIP.Spec.PodSelector, namedIPSet)
	if err != nil {
		klog.Errorf("Error programming egress IP table rules for %q: %v", key, err)

		globalEgressIP.Status.Conditions = util.TryAppendCondition(globalEgressIP.Status.Conditions, &metav1.Condition{
			Type:    string(submarinerv1.GlobalEgressIPAllocated),
			Status:  metav1.ConditionFalse,
			Reason:  "ProgramIPTableRulesFailed",
			Message: fmt.Sprintf("Error programming egress rules: %v", err),
		})

		return true
	}

	nodeName, ok := os.LookupEnv("NODE_NAME")
	if !ok {
		klog.Error("error reading the NODE_NAME from the environment")
	}

	globalEgressIP.Status.Conditions = util.TryAppendCondition(globalEgressIP.Status.Conditions, &metav1.Condition{
		Type:    string(submarinerv1.GlobalEgressIPAllocated),
		Status:  metav1.ConditionTrue,
		Reason:  "Success",
		Message: fmt.Sprintf("Processed %d global IP(s) for node: %s", numberOfIPs, nodeName),
	})

	klog.Infof("Processed %v global IP(s) for %q", globalEgressIP.Status.AllocatedIPs, key)

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

func (c *globalEgressIPController) onDelete(numRequeues int, globalEgressIP *submarinerv1.GlobalEgressIP) bool {
	key, _ := cache.MetaNamespaceKeyFunc(globalEgressIP)

	c.Lock()
	defer c.Unlock()

	podWatcher, found := c.podWatchers[key]
	if found {
		close(podWatcher.stopCh)
		delete(c.podWatchers, key)
	}

	namedIPSet := c.newNamedIPSet(key)

	requeue := c.flushGlobalEgressRules(key, namedIPSet.Name(), numRequeues, globalEgressIP)
	if requeue {
		return requeue
	}

	if err := namedIPSet.Destroy(); err != nil {
		klog.Errorf("Error destroying the ipSet %q for %q: %v", namedIPSet.Name(), key, err)

		if shouldRequeue(numRequeues) {
			return true
		}
	}

	klog.Infof("Successfully deleted all the iptables/ipset rules for %q ", key)

	return false
}

func (c *globalEgressIPController) getIPSetName(key string) string {
	hash := sha256.Sum256([]byte(key))
	encoded := base32.StdEncoding.EncodeToString(hash[:])
	// Max length of IPSet name can be 31
	return IPSetPrefix + encoded[:25]
}

func (c *globalEgressIPController) createPodWatcher(key string, namedIPSet ipset.Named, numberOfIPs int,
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

	podWatcher, err := startEgressPodWatcher(key, globalEgressIP.Namespace, namedIPSet, &c.watcherConfig, globalEgressIP.Spec.PodSelector)
	if err != nil {
		klog.Errorf("Error starting pod watcher for %q: %v", key, err)
		return false
	}

	c.podWatchers[key] = podWatcher
	podWatcher.podSelector = globalEgressIP.Spec.PodSelector

	klog.Infof("Started pod watcher for %q", key)

	return true
}

// nolint:wrapcheck  // No need to wrap these errors.
func (c *globalEgressIPController) flushGlobalEgressRules(key, ipSetName string, numRequeues int,
	globalEgressIP *submarinerv1.GlobalEgressIP,
) bool {
	return FlushAllocatedIPRules(key, numRequeues, func(allocatedIPs []string) error {
		if globalEgressIP.Spec.PodSelector != nil {
			return c.ipt.RemoveEgressRulesForPods(key, ipSetName,
				getTargetSNATIPaddress(allocatedIPs), globalNetIPTableMark)
		}

		return c.ipt.RemoveEgressRulesForNamespace(key, ipSetName, getTargetSNATIPaddress(allocatedIPs), globalNetIPTableMark)
	}, globalEgressIP.Status.AllocatedIPs...)
}

func (c *globalEgressIPController) newNamedIPSet(key string) ipset.Named {
	return ipset.NewNamed(&ipset.IPSet{
		Name:    c.getIPSetName(key),
		SetType: ipset.HashIP,
	}, c.ipSetIface)
}
