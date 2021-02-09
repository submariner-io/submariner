/*
© 2021 Red Hat, Inc. and others

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
package ipam

import (
	"fmt"

	"github.com/submariner-io/admiral/pkg/log"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
)

func (i *Controller) nodeUpdater(obj runtime.Object, key string) error {
	node := obj.(*k8sv1.Node)
	cniIfaceIP := node.GetAnnotations()[constants.CniInterfaceIP]
	existingGlobalIP := node.GetAnnotations()[SubmarinerIpamGlobalIP]
	allocatedIP, err := i.annotateGlobalIP(key, existingGlobalIP)
	if err != nil { // failed to get globalIP or failed to update, we want to retry
		logAndRequeue(key, i.nodeWorkqueue)
		return fmt.Errorf("failed to annotate globalIP to node %q: %+v", key, err)
	}

	// This case is hit in one of the two situations
	// 1. when the Worker Node does not have the globalIP annotation and a new globalIP is allocated
	// 2. when the current globalIP annotation on the Node does not match with the info maintained by ipPool
	if allocatedIP != "" {
		klog.V(log.DEBUG).Infof("Allocating globalIP %s to Node %q ", allocatedIP, key)
		err = i.syncNodeRules(node.Name, cniIfaceIP, allocatedIP, AddRules)
		if err != nil {
			logAndRequeue(key, i.nodeWorkqueue)
			return err
		}

		annotations := node.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}

		annotations[SubmarinerIpamGlobalIP] = allocatedIP

		node.SetAnnotations(annotations)

		_, err := i.kubeClientSet.CoreV1().Nodes().Update(node)
		if err != nil {
			logAndRequeue(key, i.nodeWorkqueue)
			return err
		}
	} else if existingGlobalIP != "" {
		klog.V(log.DEBUG).Infof("Node %q already has globalIP %s annotation, syncing rules", key, existingGlobalIP)
		// When Globalnet Controller is migrated, we get notification for all the existing Nodes.
		// For Worker Nodes that already have the annotation, we update the local ipPool cache and sync
		// the iptable rules on the new GatewayNode.
		// Note: This case will also be hit when Globalnet Pod is restarted
		err = i.syncNodeRules(node.Name, cniIfaceIP, existingGlobalIP, AddRules)
		if err != nil {
			logAndRequeue(key, i.nodeWorkqueue)
			return err
		}
	}

	return nil
}

func (i *Controller) handleRemovedNode(obj interface{}) {
	// TODO: further minimize duplication between this and handleRemovedPod
	var node *k8sv1.Node
	var ok bool
	var key string
	var err error
	if node, ok = obj.(*k8sv1.Node); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			klog.Errorf("Could not convert object %v to Node", obj)
			return
		}

		node, ok = tombstone.Obj.(*k8sv1.Node)
		if !ok {
			klog.Errorf("Could not convert object tombstone %v to Node", tombstone.Obj)
			return
		}
	}

	globalIP := node.Annotations[SubmarinerIpamGlobalIP]
	cniIfaceIP := node.Annotations[constants.CniInterfaceIP]
	if globalIP != "" && cniIfaceIP != "" {
		if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
			utilruntime.HandleError(err)
			return
		}

		i.pool.Release(key)
		klog.V(log.DEBUG).Infof("Released ip %s for Node %s", globalIP, key)

		err = i.syncNodeRules(node.Name, cniIfaceIP, globalIP, DeleteRules)
		if err != nil {
			klog.Errorf("Error while cleaning up HostNetwork egress rules. %v", err)
		}
	} else {
		klog.V(log.DEBUG).Infof("handleRemovedNode called for %q, that has globalIP %s and cniIfaceIP %s", key, globalIP, cniIfaceIP)
	}
}
