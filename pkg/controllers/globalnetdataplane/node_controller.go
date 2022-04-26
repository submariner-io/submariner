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
	"context"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/syncer"
	admUtil "github.com/submariner-io/admiral/pkg/util"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/iptables"
	routeAgent "github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
)

func NewNodeController(config *syncer.ResourceSyncerConfig, nodeName string) (Interface, error) {
	// We'll panic if config is nil, this is intentional
	var err error

	klog.Infof("Creating Globalnet Node datapath controller for node %s", nodeName)

	controller := &nodeController{
		baseSyncerController: newBaseSyncerController(),
		nodeName:             nodeName,
	}

	iptIface, err := iptables.New()
	if err != nil {
		return nil, errors.Wrap(err, "error creating the IPTablesInterface handler")
	}

	controller.ipt = iptIface

	// We only want to receive events for our node's node object
	federator := federate.NewUpdateFederator(config.SourceClient, config.RestMapper, corev1.NamespaceAll)
	controller.resourceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:                "Local Node syncer",
		ResourceType:        &corev1.Node{},
		SourceClient:        config.SourceClient,
		SourceNamespace:     corev1.NamespaceAll,
		RestMapper:          config.RestMapper,
		Federator:           federator,
		Scheme:              config.Scheme,
		Transform:           controller.process,
		ResourcesEquivalent: controller.onNodeUpdated,
		SourceLabelSelector: "submariner.io/gateway",
		ShouldProcess:       shouldProcessClusterGlobalEgressIP,
	})

	if err != nil {
		return nil, errors.Wrap(err, "error creating the federator")
	}

	_, gvr, err := admUtil.ToUnstructuredResource(&corev1.Node{}, config.RestMapper)
	if err != nil {
		return nil, errors.Wrap(err, "error converting resource")
	}

	controller.nodes = config.SourceClient.Resource(*gvr)

	localNodeInfo, err := controller.nodes.Get(context.TODO(), controller.nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, errors.Wrapf(err, "error retrieving local Node object %q", controller.nodeName)
	}

	existingGlobalIP := localNodeInfo.GetAnnotations()[constants.SmGlobalIP]
	if existingGlobalIP == "" {
		klog.Infof("existingGlobalIP annotation on node %q is not set", nodeName)
		return controller, nil
	}

	cniIfaceIP := localNodeInfo.GetAnnotations()[routeAgent.CNIInterfaceIP]
	if cniIfaceIP == "" {
		// To support Gateway healthCheck, globalnet requires the cniIfaceIP of the respective node.
		// Route-agent running on the node annotates the respective node with the cniIfaceIP.
		// In this API, we check for the presence of this annotation and process the node only
		// when the annotation exists.
		klog.Infof("cniIfaceIP annotation on node %q is currently missing", nodeName)
		return controller, nil
	}

	return controller, nil
}

func (n *nodeController) process(from runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
	node := from.(*corev1.Node)

	klog.Infof("Processing local %sd Node %q", op, node.Name)

	return n.setupRules(node)
}

func (n *nodeController) setupRules(node *corev1.Node) (runtime.Object, bool) {
	cniIfaceIP := node.GetAnnotations()[routeAgent.CNIInterfaceIP]
	if cniIfaceIP == "" {
		// To support connectivity from HostNetwork to remoteCluster, globalnet requires the
		// cniIfaceIP of the respective node. Route-agent running on the node annotates the
		// respective node with the cniIfaceIP. In this API, we check for the presence of this
		// annotation and process the node event only when the annotation exists.
		klog.Infof("No cniIfaceIP allocated for node %q, writing no rules for globalnet", node.Name)
		return nil, false
	}

	globalIP := node.GetAnnotations()[constants.SmGlobalIP]
	if globalIP == "" {
		klog.Infof("No global IP allocated for node %q, writing no rules for globalnet", node.Name)
		return nil, false
	}

	klog.Infof("Adding ingress rules for node %q with global IP %s, CNI IP %s", node.Name, globalIP, cniIfaceIP)

	if err := n.ipt.AddIngressRulesForHealthCheck(cniIfaceIP, globalIP); err != nil {
		klog.Errorf("Error programming rules for Gateway healthcheck on node %q: %v", node.Name, err)

		return nil, true
	}

	return nil, false
}

// This should return false if either CNIIface IP or new GlobalIP has changed.
func (n *nodeController) onNodeUpdated(oldObj, newObj *unstructured.Unstructured) bool {
	if oldObj.GetName() != n.nodeName {
		return true
	}

	oldCNIIfaceIPOnNode := oldObj.GetAnnotations()[routeAgent.CNIInterfaceIP]
	newCNIIfaceIPOnNode := newObj.GetAnnotations()[routeAgent.CNIInterfaceIP]
	oldGlobalIPOnNode := oldObj.GetAnnotations()[constants.SmGlobalIP]
	newGlobalIPOnNode := newObj.GetAnnotations()[constants.SmGlobalIP]

	// globalIPCleared := oldGlobalIPOnNode != "" && newGlobalIPOnNode == ""
	// if globalIPCleared || oldCNIIfaceIPOnNode != newCNIIfaceIPOnNode {
	// 	if oldCNIIfaceIPOnNode != "" && oldGlobalIPOnNode != "" {
	// 		if err := n.ipt.RemoveIngressRulesForHealthCheck(oldCNIIfaceIPOnNode, oldGlobalIPOnNode); err != nil {
	// 			klog.Errorf("Error deleting rules for Gateway healthcheck on node %q: %v", n.nodeName, err)
	// 		}
	// 	}

	// 	return false
	// }

	return (oldCNIIfaceIPOnNode == newCNIIfaceIPOnNode) && (oldGlobalIPOnNode == newGlobalIPOnNode)
}
