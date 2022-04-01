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

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/syncer"
	admUtil "github.com/submariner-io/admiral/pkg/util"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/ipam"
	routeAgent "github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
)

func NewNodeController(config *syncer.ResourceSyncerConfig, pool *ipam.IPPool) (Interface, error) {
	// We'll panic if config is nil, this is intentional
	var err error

	klog.Info("Creating Node controller")

	controller := &nodeController{
		baseIPAllocationController: newBaseIPAllocationController(pool),
	}

	federator := federate.NewUpdateFederator(config.SourceClient, config.RestMapper, corev1.NamespaceAll)
	// Setup resource syncer for only GW nodes
	controller.resourceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:                "Node syncer",
		ResourceType:        &corev1.Node{},
		SourceClient:        config.SourceClient,
		SourceNamespace:     corev1.NamespaceAll,
		RestMapper:          config.RestMapper,
		Federator:           federator,
		Scheme:              config.Scheme,
		Transform:           controller.process,
		ResourcesEquivalent: controller.onNodeUpdated,
		SourceLabelSelector: "submariner.io/gateway",
	})

	if err != nil {
		return nil, errors.Wrap(err, "error creating the federator")
	}

	_, gvr, err := admUtil.ToUnstructuredResource(&corev1.Node{}, config.RestMapper)
	if err != nil {
		return nil, errors.Wrap(err, "error converting resource")
	}

	controller.nodes = config.SourceClient.Resource(*gvr)

	gatewayNodes, err := controller.nodes.List(context.TODO(), metav1.ListOptions{LabelSelector: "submariner.io/gateway"})
	if err != nil {
		return nil, errors.Wrap(err, "error retrieving gateway nodes")
	}

	// Reserve allocated IPs for GW nodes
	if err := controller.reserveAllocatedIP(federator, gatewayNodes); err != nil {
		return nil, err
	}

	return controller, nil
}

func (n *nodeController) process(from runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	node := from.(*corev1.Node)

	// If there's an existing Global IP and the gw node is getting deleted un-allocate it
	if existingGlobalIP := node.GetAnnotations()[constants.SmGlobalIP]; existingGlobalIP != "" {
		if op == syncer.Delete {
			_ = n.pool.Release(existingGlobalIP)
			return nil, false
		}
	}

	klog.Infof("Processing %sd Node %q", op, node.Name)

	// Alocate IP if needed
	return n.allocateIP(node, op)
}

func (n *nodeController) allocateIP(node *corev1.Node, op syncer.Operation) (runtime.Object, bool) {
	cniIfaceIP := node.GetAnnotations()[routeAgent.CNIInterfaceIP]
	if cniIfaceIP == "" {
		// To support connectivity from HostNetwork to remoteCluster, globalnet requires the
		// cniIfaceIP of the respective node. Route-agent running on the node annotates the
		// respective node with the cniIfaceIP. In this API, we check for the presence of this
		// annotation and process the node event only when the annotation exists.
		return nil, false
	}

	globalIP := node.GetAnnotations()[constants.SmGlobalIP]
	if op == syncer.Create && globalIP != "" {
		return nil, false
	}

	if globalIP == "" {
		ips, err := n.pool.Allocate(1)
		if err != nil {
			klog.Errorf("Error allocating IPs for node %q: %v", node.Name, err)
			return nil, true
		}

		globalIP = ips[0]

		klog.Infof("Allocated global IP %s for node %q", globalIP, node.Name)
	}

	return n.updateNodeAnnotation(node, globalIP), false
}

func (n *nodeController) reserveAllocatedIP(federator federate.Federator, objs *unstructured.UnstructuredList) error {
	for _, obj := range objs.Items {
		existingGlobalIP := obj.GetAnnotations()[constants.SmGlobalIP]
		if existingGlobalIP == "" {
			return nil
		}

		cniIfaceIP := obj.GetAnnotations()[routeAgent.CNIInterfaceIP]
		if cniIfaceIP == "" {
			// To support Gateway healthCheck, globalnet requires the cniIfaceIP of the respective node.
			// Route-agent running on the node annotates the respective node with the cniIfaceIP.
			// In this API, we check for the presence of this annotation and process the node only
			// when the annotation exists.
			klog.Infof("cniIfaceIP annotation on node %q is currently missing", obj.GetName())
			return nil
		}

		err := n.pool.Reserve(existingGlobalIP)
		if err != nil {
			klog.Warningf("Could not reserve allocated GlobalIP for Node %q: %v", obj.GetName(), err)

			return errors.Wrap(federator.Distribute(n.updateNodeAnnotation(&obj, "")), "error updating the Node global IP annotation") // nolint // TODO MAG POC
		}
	}

	return nil
}

func (n *nodeController) updateNodeAnnotation(node runtime.Object, globalIP string) runtime.Object {
	objMeta, _ := meta.Accessor(node)

	annotations := objMeta.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}

	if globalIP == "" {
		delete(annotations, constants.SmGlobalIP)
	} else {
		annotations[constants.SmGlobalIP] = globalIP
	}

	objMeta.SetAnnotations(annotations)

	return node
}

// Events for all GW nodes.
func (n *nodeController) onNodeUpdated(oldObj, newObj *unstructured.Unstructured) bool {
	oldCNIIfaceIPOnNode := oldObj.GetAnnotations()[routeAgent.CNIInterfaceIP]
	newCNIIfaceIPOnNode := newObj.GetAnnotations()[routeAgent.CNIInterfaceIP]
	oldGlobalIPOnNode := oldObj.GetAnnotations()[constants.SmGlobalIP]
	newGlobalIPOnNode := newObj.GetAnnotations()[constants.SmGlobalIP]

	globalIPCleared := oldGlobalIPOnNode != "" && newGlobalIPOnNode == ""
	if globalIPCleared || oldCNIIfaceIPOnNode != newCNIIfaceIPOnNode {
		if globalIPCleared {
			_ = n.pool.Release(oldGlobalIPOnNode)
		}

		return false
	}

	return true
}
