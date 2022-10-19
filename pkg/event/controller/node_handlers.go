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

package controller

import (
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func (c *Controller) handleRemovedNode(obj runtime.Object, numRequeues int) bool {
	node := obj.(*k8sv1.Node)

	if err := c.handlers.NodeRemoved(node); err != nil {
		logger.Error(err, "Error handling removed Node")
		return true
	}

	return false
}

func (c *Controller) handleCreatedNode(obj runtime.Object, numRequeues int) bool {
	node := obj.(*k8sv1.Node)

	if err := c.handlers.NodeCreated(node); err != nil {
		logger.Error(err, "Error handling created Node")
		return true
	}

	return false
}

func (c *Controller) handleUpdatedNode(obj runtime.Object, numRequeues int) bool {
	node := obj.(*k8sv1.Node)

	if err := c.handlers.NodeUpdated(node); err != nil {
		logger.Error(err, "Error handling updated Node")
		return true
	}

	return false
}

func (c *Controller) isNodeEquivalent(obj1, obj2 *unstructured.Unstructured) bool {
	// TODO: filter on changes for labels, annotations, podcidr, podcidrs, addresses
	return false
}
