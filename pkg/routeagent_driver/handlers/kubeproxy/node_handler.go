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
	"net"

	"github.com/submariner-io/admiral/pkg/log"
	k8sV1 "k8s.io/api/core/v1"
	k8snet "k8s.io/utils/net"
)

func (kp *SyncHandler) NodeCreated(node *k8sV1.Node) error {
	logger.V(log.DEBUG).Infof("A Node with name %q and addresses %#v has been added to the cluster",
		node.Name, node.Status.Addresses)

	for i, addr := range node.Status.Addresses {
		// Revisit when IPv6 support is added.
		if addr.Type == k8sV1.NodeInternalIP && k8snet.IsIPv4String(node.Status.Addresses[i].Address) {
			kp.populateRemoteVtepIps(node.Status.Addresses[i].Address, Add)
			break
		}
	}

	return nil
}

func (kp *SyncHandler) NodeRemoved(node *k8sV1.Node) error {
	logger.V(log.DEBUG).Infof("A Node with name %q has been removed", node.Name)

	for i, addr := range node.Status.Addresses {
		if addr.Type == k8sV1.NodeInternalIP {
			kp.populateRemoteVtepIps(node.Status.Addresses[i].Address, Delete)
			break
		}
	}

	return nil
}

func (kp *SyncHandler) populateRemoteVtepIps(vtepIP string, operation Operation) {
	// The remoteVTEP info is cached on all the routeAgent nodes and is used when there is a Gateway transition.
	if operation == Add && !kp.remoteVTEPs.Has(vtepIP) {
		kp.remoteVTEPs.Insert(vtepIP)
	} else if operation == Delete {
		kp.remoteVTEPs.Delete(vtepIP)
	}

	isOnGateway := kp.State().IsOnGateway()

	logger.V(log.DEBUG).Infof("populateRemoteVtepIps is called with vtepIP %s, isGatewayNode %t", vtepIP, isOnGateway)

	if isOnGateway {
		switch operation {
		case Add:
			if err := kp.vxlanDevice.AddFDB(net.ParseIP(vtepIP), "00:00:00:00:00:00"); err != nil {
				logger.Errorf(err, "Failed to add FDB entry on the Gateway Node vxlan iface")
			}
		case Delete:
			if err := kp.vxlanDevice.DelFDB(net.ParseIP(vtepIP), "00:00:00:00:00:00"); err != nil {
				logger.Errorf(err, "Failed to delete FDB entry on the Gateway Node vxlan iface")
			}
		case Flush:
		}
	}
}
