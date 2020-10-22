package controller

import (
	"github.com/submariner-io/admiral/pkg/util"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
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
	return equality.Semantic.DeepEqual(util.GetNestedField(obj1, "Addresses"),
		util.GetNestedField(obj2, "Addresses"))
}
