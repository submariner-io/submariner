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
	"slices"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/vxlan"
	k8sV1 "k8s.io/api/core/v1"
	k8snet "k8s.io/utils/net"
)

func (kp *SyncHandler) NodeCreated(node *k8sV1.Node) error {
	return kp.syncRemoteNode(node, Add)
}

func (kp *SyncHandler) NodeUpdated(node *k8sV1.Node) error {
	return kp.syncRemoteNode(node, Add)
}

func (kp *SyncHandler) NodeRemoved(node *k8sV1.Node) error {
	return kp.syncRemoteNode(node, Delete)
}

func (kp *SyncHandler) syncRemoteNode(node *k8sV1.Node, operation Operation) error {
	logger.V(log.DEBUG).Infof("Syncing node %q (operation=%v), addresses %#v, podCIDR=%q",
		node.Name, operation, node.Status.Addresses, node.Spec.PodCIDR)

	internalIP := kp.nodeInternalIP(node)
	if internalIP == "" {
		return nil
	}

	localIP, err := kp.getHostIfaceIPAddress()
	if err == nil && localIP.Equal(net.ParseIP(internalIP)) {
		logger.V(log.DEBUG).Infof("Skipping local node IP %s", internalIP)
		return nil
	}

	podCIDRs := kp.nodePodCIDRs(node)

	return kp.populateRemoteNode(internalIP, podCIDRs, operation)
}

func (kp *SyncHandler) nodeInternalIP(node *k8sV1.Node) string {
	for _, addr := range node.Status.Addresses {
		if addr.Type == k8sV1.NodeInternalIP && k8snet.IPFamilyOfString(addr.Address) == kp.ipFamily {
			return addr.Address
		}
	}

	return ""
}

func (kp *SyncHandler) nodePodCIDRs(node *k8sV1.Node) []string {
	var cidrs []string

	// Prefer PodCIDRs when set — its first entry normally duplicates PodCIDR.
	if len(node.Spec.PodCIDRs) > 0 {
		for _, podCIDR := range node.Spec.PodCIDRs {
			if k8snet.IPFamilyOfCIDRString(podCIDR) == kp.ipFamily {
				cidrs = append(cidrs, podCIDR)
			}
		}

		return cidrs
	}

	if node.Spec.PodCIDR != "" && k8snet.IPFamilyOfCIDRString(node.Spec.PodCIDR) == kp.ipFamily {
		cidrs = append(cidrs, node.Spec.PodCIDR)
	}

	return cidrs
}

func (kp *SyncHandler) populateRemoteNode(internalIP string, podCIDRs []string, operation Operation) error {
	if operation == Add {
		if existing, ok := kp.remoteNodePodCIDRs[internalIP]; ok && slices.Equal(existing, podCIDRs) {
			return nil
		}

		kp.remoteVTEPs.Insert(internalIP)
		kp.remoteNodePodCIDRs[internalIP] = podCIDRs
	} else if operation == Delete {
		kp.remoteVTEPs.Delete(internalIP)
		delete(kp.remoteNodePodCIDRs, internalIP)
	}

	if !kp.State().IsOnGateway() || kp.vxlanDevice == nil {
		logger.V(log.DEBUG).Infof("populateRemoteNode %s cached only (gateway=%t device=%v)",
			internalIP, kp.State().IsOnGateway(), kp.vxlanDevice != nil)

		return nil
	}

	return kp.programRemoteNodeOnGateway(internalIP, podCIDRs, operation)
}

func (kp *SyncHandler) programRemoteNodeOnGateway(internalIP string, podCIDRs []string, operation Operation) error {
	underlayIP := net.ParseIP(internalIP)
	if underlayIP == nil {
		return errors.Errorf("invalid node internal IP %q", internalIP)
	}

	hwAddr := vxlan.HardwareAddrFromIP(underlayIP)
	if hwAddr == nil {
		return errors.Errorf("unable to derive MAC for node IP %q", internalIP)
	}

	hwAddrStr := hwAddr.String()

	vtepIP, err := vxlan.GetVtepIPAddressFrom(internalIP, kp.vtepPrefixCIDR, kp.ipFamily)
	if err != nil {
		return errors.Wrapf(err, "failed to derive VTEP IP for %s", internalIP)
	}

	logger.V(log.DEBUG).Infof("Programming gateway datapath for node %s (vtep=%s mac=%s podCIDRs=%v op=%v)",
		internalIP, vtepIP, hwAddrStr, podCIDRs, operation)

	switch operation {
	case Add:
		// Remove legacy flood FDB entries that cause reply duplication.
		_ = kp.vxlanDevice.DelFDB(underlayIP, "00:00:00:00:00:00")

		if err := kp.vxlanDevice.AddFDB(underlayIP, hwAddrStr); err != nil {
			return errors.Wrapf(err, "failed to add unicast FDB for %s", internalIP)
		}

		if err := kp.vxlanDevice.AddNeighbor(vtepIP, hwAddrStr); err != nil {
			return errors.Wrapf(err, "failed to add VTEP neighbor for %s", vtepIP)
		}

		return kp.updateIngressRoutesForNode(vtepIP, podCIDRs, Add)
	case Delete:
		if err := kp.updateIngressRoutesForNode(vtepIP, podCIDRs, Delete); err != nil {
			logger.Errorf(err, "Failed to delete ingress routes for node %s", internalIP)
		}

		if err := kp.vxlanDevice.DelNeighbor(vtepIP, hwAddrStr); err != nil {
			logger.Errorf(err, "Failed to delete VTEP neighbor for %s", vtepIP)
		}

		if err := kp.vxlanDevice.DelFDB(underlayIP, hwAddrStr); err != nil {
			logger.Errorf(err, "Failed to delete FDB for %s", internalIP)
		}

		_ = kp.vxlanDevice.DelFDB(underlayIP, "00:00:00:00:00:00")

		return nil
	case Flush:
		return nil
	}

	return nil
}

func (kp *SyncHandler) updateIngressRoutesForNode(vtepIP net.IP, podCIDRs []string, operation Operation) error {
	if len(podCIDRs) == 0 {
		return nil
	}

	destCIDRs := make([]net.IPNet, 0, len(podCIDRs))

	for _, podCIDR := range podCIDRs {
		_, ipNet, err := net.ParseCIDR(podCIDR)
		if err != nil {
			logger.Errorf(err, "Invalid pod CIDR %q", podCIDR)
			continue
		}

		destCIDRs = append(destCIDRs, *ipNet)
	}

	if len(destCIDRs) == 0 {
		return nil
	}

	switch operation {
	case Add:
		return errors.Wrap(kp.vxlanDevice.AddRoutes(vtepIP, nil, 0, destCIDRs...), "failed to add ingress routes")
	case Delete:
		return errors.Wrap(kp.vxlanDevice.DelRoutes(0, destCIDRs...), "failed to delete ingress routes")
	case Flush:
		return nil
	}

	return nil
}

func (kp *SyncHandler) syncAllRemoteNodesOnGateway() {
	if kp.vxlanDevice == nil {
		return
	}

	for internalIP, podCIDRs := range kp.remoteNodePodCIDRs {
		if err := kp.programRemoteNodeOnGateway(internalIP, podCIDRs, Add); err != nil {
			logger.Errorf(err, "Failed to program gateway datapath for remote node %s", internalIP)
		}
	}
}

func (kp *SyncHandler) clearAllRemoteNodesOnGateway() {
	if kp.vxlanDevice == nil {
		return
	}

	for internalIP, podCIDRs := range kp.remoteNodePodCIDRs {
		if err := kp.programRemoteNodeOnGateway(internalIP, podCIDRs, Delete); err != nil {
			logger.Errorf(err, "Failed to clear gateway datapath for remote node %s", internalIP)
		}
	}
}
