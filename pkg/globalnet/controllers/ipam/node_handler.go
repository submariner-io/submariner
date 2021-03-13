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
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
)

func (i *Controller) onNodeUpdated(oldObj, newObj *unstructured.Unstructured) bool {
	oldCNIIfaceIPOnNode := oldObj.GetAnnotations()[constants.CNIInterfaceIP]
	newCNIIfaceIPOnNode := newObj.GetAnnotations()[constants.CNIInterfaceIP]

	return oldCNIIfaceIPOnNode == newCNIIfaceIPOnNode && i.areGlobalIPsEquivalent(oldObj, newObj)
}

func (i *Controller) processNode(from runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	node := from.(*k8sv1.Node)

	if op == syncer.Delete {
		i.processRemovedNode(node)
		return nil, false
	}

	cniIfaceIP := node.GetAnnotations()[constants.CNIInterfaceIP]
	if cniIfaceIP == "" {
		// To support connectivity from HostNetwork to remoteCluster, globalnet requires the
		// cniIfaceIP of the respective node. Route-agent running on the node annotates the
		// respective node with the cniIfaceIP. In this API, we check for the presence of this
		// annotation and process the node event only when the annotation exists.
		return nil, true
	}

	return i.updateNode(node)
}

func (i *Controller) processRemovedNode(node *k8sv1.Node) {
	globalIP := node.Annotations[SubmarinerIPAMGlobalIP]
	cniIfaceIP := node.Annotations[constants.CNIInterfaceIP]
	if globalIP != "" && cniIfaceIP != "" {
		key, _ := cache.MetaNamespaceKeyFunc(node)

		i.pool.Release(key)

		klog.V(log.DEBUG).Infof("Released globalIP %s for Node %q", globalIP, key)

		err := i.syncNodeRules(node.Name, cniIfaceIP, globalIP, DeleteRules)
		if err != nil {
			klog.Errorf("Error while cleaning up Node egress rules: %v", err)
		}
	}
}

func (i *Controller) updateNode(node *k8sv1.Node) (runtime.Object, bool) {
	cniIfaceIP := node.GetAnnotations()[constants.CNIInterfaceIP]

	return i.updateGlobalIP(node, func(globalIP string) error {
		return i.syncNodeRules(node.Name, cniIfaceIP, globalIP, AddRules)
	})
}
