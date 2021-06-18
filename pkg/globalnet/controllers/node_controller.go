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
	"github.com/submariner-io/submariner/pkg/globalnet/controllers/iptables"
	"github.com/submariner-io/submariner/pkg/ipam"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
)

func NewNodeController(config syncer.ResourceSyncerConfig, pool *ipam.IPPool, nodeName string) (Interface, error) {
	var err error

	klog.Info("Creating Node controller")

	iptIface, err := iptables.New()
	if err != nil {
		return nil, errors.WithMessage(err, "error creating the IPTablesInterface handler")
	}

	controller := &nodeController{
		baseIPAllocationController: newBaseIPAllocationController(pool, iptIface),
		nodeName:                   nodeName,
	}

	federator := federate.NewUpdateFederator(config.SourceClient, config.RestMapper, corev1.NamespaceAll)
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
	})

	if err != nil {
		return nil, err
	}

	_, gvr, err := admUtil.ToUnstructuredResource(&corev1.Node{}, config.RestMapper)
	if err != nil {
		return nil, err
	}

	controller.nodes = config.SourceClient.Resource(*gvr)
	localNodeInfo, err := controller.nodes.Get(context.TODO(), controller.nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, errors.WithMessagef(err, "error retrieving local Node %q", controller.nodeName)
	}

	if err := controller.reserveAllocatedIP(federator, localNodeInfo); err != nil {
		return nil, err
	}

	return controller, nil
}

func (n *nodeController) process(from runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	node := from.(*corev1.Node)

	// If the event corresponds to a different node which has globalIP annotation, release the globalIP back to Pool.
	if node.Name != n.nodeName {
		if existingGlobalIP := node.GetAnnotations()[GlobalIPKey]; existingGlobalIP != "" {
			if op == syncer.Delete {
				_ = n.pool.Release(existingGlobalIP)
				return nil, false
			} else {
				_ = n.pool.Release(existingGlobalIP)
				return n.updateNodeAnnotation(node, "")
			}
		}

		return nil, false
	}

	cniIfaceIP := node.GetAnnotations()[constants.CNIInterfaceIP]
	if cniIfaceIP == "" {
		// To support connectivity from HostNetwork to remoteCluster, globalnet requires the
		// cniIfaceIP of the respective node. Route-agent running on the node annotates the
		// respective node with the cniIfaceIP. In this API, we check for the presence of this
		// annotation and process the node event only when the annotation exists.
		return nil, false
	}

	klog.Infof("Processing %sd Node %q", op, node.Name)

	return n.onCreateOrUpdate(node)
}

func (n *nodeController) onCreateOrUpdate(node *corev1.Node) (runtime.Object, bool) {
	existingGlobalIP := node.GetAnnotations()[GlobalIPKey]
	if existingGlobalIP != "" {
		return nil, false
	}

	cniIfaceIP := node.GetAnnotations()[constants.CNIInterfaceIP]
	globalIP, err := n.pool.Allocate(1)
	if err != nil {
		klog.Errorf("Error allocating IPs for node %q: %v", node.Name, err)
		return nil, true
	}

	if err := n.iptIface.AddIngressRulesForHealthCheck(cniIfaceIP, globalIP[0]); err != nil {
		klog.Errorf("Error programming rules for Gateway healthcheck on node %q: %v", node.Name, err)

		_ = n.pool.Release(globalIP[0])

		return nil, true
	}

	return n.updateNodeAnnotation(node, globalIP[0])
}

func (n *nodeController) reserveAllocatedIP(federator federate.Federator, obj *unstructured.Unstructured) error {
	existingGlobalIP := obj.GetAnnotations()[GlobalIPKey]
	if existingGlobalIP == "" {
		return nil
	}

	cniIfaceIP := obj.GetAnnotations()[constants.CNIInterfaceIP]
	if cniIfaceIP == "" {
		// To support Gateway healthCheck, globalnet requires the cniIfaceIP of the respective node.
		// Route-agent running on the node annotates the respective node with the cniIfaceIP.
		// In this API, we check for the presence of this annotation and process the node only
		// when the annotation exists.
		klog.Infof("cniIfaceIP annotation on node %q is currently missing", n.nodeName)
		return nil
	}

	err := n.pool.Reserve(existingGlobalIP)
	if err == nil {
		err = n.iptIface.AddIngressRulesForHealthCheck(cniIfaceIP, existingGlobalIP)
		if err != nil {
			_ = n.pool.Release(existingGlobalIP)
		}
	}

	if err != nil {
		klog.Warningf("Could not reserve allocated GlobalIP for Node %q: %v", obj.GetName(), err)
		annotations := obj.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}

		delete(annotations, GlobalIPKey)
		obj.SetAnnotations(annotations)

		return federator.Distribute(obj)
	}

	return nil
}

func (n *nodeController) updateNodeAnnotation(node *corev1.Node, globalIP string) (runtime.Object, bool) {
	annotations := node.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}

	if globalIP == "" {
		delete(annotations, GlobalIPKey)
	} else {
		annotations[GlobalIPKey] = globalIP
	}

	node.SetAnnotations(annotations)

	return node, false
}

func (n *nodeController) onNodeUpdated(oldObj, newObj *unstructured.Unstructured) bool {
	if oldObj.GetName() != n.nodeName {
		return true
	}

	oldCNIIfaceIPOnNode := oldObj.GetAnnotations()[constants.CNIInterfaceIP]
	newCNIIfaceIPOnNode := newObj.GetAnnotations()[constants.CNIInterfaceIP]
	oldGlobalIPOnNode := oldObj.GetAnnotations()[GlobalIPKey]
	newGlobalIPOnNode := newObj.GetAnnotations()[GlobalIPKey]

	if oldGlobalIPOnNode != "" && newGlobalIPOnNode == "" {
		_ = n.pool.Release(oldGlobalIPOnNode)

		if err := n.iptIface.RemoveIngressRulesForHealthCheck(oldCNIIfaceIPOnNode, oldGlobalIPOnNode); err != nil {
			klog.Errorf("Error deleting rules for Gateway healthcheck on node %q: %v", n.nodeName, err)
			return false
		}
	}

	return oldCNIIfaceIPOnNode == newCNIIfaceIPOnNode && newGlobalIPOnNode != ""
}
