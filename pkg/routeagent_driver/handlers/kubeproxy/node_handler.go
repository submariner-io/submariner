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

package kubeproxy

import (
	"github.com/submariner-io/admiral/pkg/log"
	k8sV1 "k8s.io/api/core/v1"
	"k8s.io/klog"
)

func (kp *SyncHandler) NodeCreated(node *k8sV1.Node) error {
	klog.V(log.DEBUG).Infof("A Node with name %q and addresses %#v has been added to the cluster",
		node.Name, node.Status.Addresses)

	kp.syncHandlerMutex.Lock()
	defer kp.syncHandlerMutex.Unlock()

	for i, addr := range node.Status.Addresses {
		if addr.Type == k8sV1.NodeInternalIP {
			// If the address is a GW node and this is already a GW node handler,
			// shortcircut and don't add FDB policy
			_, ok := node.Labels["submariner.io/gateway"]
			kp.populateRemoteVtepIps(node.Status.Addresses[i].Address, Add, ok)
			break
		}
	}

	return nil
}

func (kp *SyncHandler) NodeUpdated(node *k8sV1.Node) error {
	return nil
}

func (kp *SyncHandler) NodeRemoved(node *k8sV1.Node) error {
	klog.V(log.DEBUG).Infof("A Node with name %q has been removed", node.Name)

	kp.syncHandlerMutex.Lock()
	defer kp.syncHandlerMutex.Unlock()

	for i, addr := range node.Status.Addresses {
		if addr.Type == k8sV1.NodeInternalIP {
			// always try and remove all addresses here
			kp.populateRemoteVtepIps(node.Status.Addresses[i].Address, Delete, false)
			break
		}
	}

	return nil
}

func (kp *SyncHandler) populateRemoteVtepIps(vtepIP string, operation Operation, isGwAddr bool) {
	// The remoteVTEP info is cached on all the routeAgent nodes and is used when there is a Gateway transition.
	if operation == Add {
		kp.remoteVTEPs.Add(vtepIP)
	} else if operation == Delete {
		kp.remoteVTEPs.Remove(vtepIP)
	}

	klog.V(log.DEBUG).Infof("populateRemoteVtepIps is called with vtepIP %s, isGatewayNodeAddress %t, isGatewayNode %t",
		vtepIP, isGwAddr, kp.isGatewayNode)

	// Only update rules on GW nodes when newly added node is not a GW
	if kp.isGatewayNode && !isGwAddr {
		// creates or updates the physical interface and FDB Rules
		err := kp.updateVxLANInterface()
		if err != nil {
			klog.Fatalf("Unable to update VxLAN interface on non-GatewayNode (%s): %v", kp.hostname, err)
		}
	}
}
