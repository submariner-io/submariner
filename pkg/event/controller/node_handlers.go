package controller

import (
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
)

func (c *Controller) handleRemovedNode(obj runtime.Object) bool {
	node := obj.(*k8sv1.Node)

	if err := c.handlers.NodeRemoved(node); err != nil {
		klog.Errorf("Error handling removed Node: %v", err)
		return true
	}

	return false
}

func (c *Controller) handleCreatedNode(obj runtime.Object) bool {
	node := obj.(*k8sv1.Node)

	if err := c.handlers.NodeCreated(node); err != nil {
		klog.Errorf("Error handling created Node: %v", err)
		return true
	}

	return false
}

func (c *Controller) handleUpdatedNode(obj runtime.Object) bool {
	node := obj.(*k8sv1.Node)

	if err := c.handlers.NodeUpdated(node); err != nil {
		klog.Errorf("Error handling updated Node: %v", err)
		return true
	}

	return false
}

func (c *Controller) isNodeEquivalent(obj1, obj2 *unstructured.Unstructured) bool {
	// TODO: filter on changes for labels, annotations, podcidr, podcidrs, addresses
	return false
}
