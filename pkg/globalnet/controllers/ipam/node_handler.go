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
	existingGlobalIp := node.GetAnnotations()[submarinerIpamGlobalIp]
	allocatedIp, err := i.annotateGlobalIp(key, existingGlobalIp)
	if err != nil { // failed to get globalIp or failed to update, we want to retry
		logAndRequeue(key, i.nodeWorkqueue)
		return fmt.Errorf("failed to annotate globalIp to node %q: %+v", key, err)
	}

	// This case is hit in one of the two situations
	// 1. when the Worker Node does not have the globalIp annotation and a new globalIp is allocated
	// 2. when the current globalIp annotation on the Node does not match with the info maintained by ipPool
	if allocatedIp != "" {
		klog.V(log.DEBUG).Infof("Allocating globalIp %s to Node %q ", allocatedIp, key)
		err = i.syncNodeRules(node.Name, cniIfaceIP, allocatedIp, AddRules)
		if err != nil {
			logAndRequeue(key, i.nodeWorkqueue)
			return err
		}

		annotations := node.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}

		annotations[submarinerIpamGlobalIp] = allocatedIp

		node.SetAnnotations(annotations)

		_, err := i.kubeClientSet.CoreV1().Nodes().Update(node)
		if err != nil {
			logAndRequeue(key, i.nodeWorkqueue)
			return err
		}
	} else if existingGlobalIp != "" {
		klog.V(log.DEBUG).Infof("Node %q already has globalIp %s annotation, syncing rules", key, existingGlobalIp)
		// When Globalnet Controller is migrated, we get notification for all the existing Nodes.
		// For Worker Nodes that already have the annotation, we update the local ipPool cache and sync
		// the iptable rules on the new GatewayNode.
		// Note: This case will also be hit when Globalnet Pod is restarted
		err = i.syncNodeRules(node.Name, cniIfaceIP, existingGlobalIp, AddRules)
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

	globalIp := node.Annotations[submarinerIpamGlobalIp]
	cniIfaceIp := node.Annotations[constants.CniInterfaceIP]
	if globalIp != "" && cniIfaceIp != "" {
		if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
			utilruntime.HandleError(err)
			return
		}

		i.pool.Release(key)
		klog.V(log.DEBUG).Infof("Released ip %s for Node %s", globalIp, key)

		err = i.syncNodeRules(node.Name, cniIfaceIp, globalIp, DeleteRules)
		if err != nil {
			klog.Errorf("Error while cleaning up HostNetwork egress rules. %v", err)
		}
	} else {
		klog.V(log.DEBUG).Infof("handleRemovedNode called for %q, that has globalIp %s and cniIfaceIp %s", key, globalIp, cniIfaceIp)
	}
}
