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

package cilium

import (
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/submariner/pkg/netlink"
	corev1 "k8s.io/api/core/v1"
	k8snet "k8s.io/utils/net"
)

const (
	labelControlPlane = "node-role.kubernetes.io/control-plane"
	labelMaster       = "node-role.kubernetes.io/master"

	// ciliumHostDevice is Cilium's per-node router interface. Its IPv4 is local
	// but distinct from NodeInternalIP, so it can be used as HostIP on the
	// Submariner gateway without Cilium clearing tunnelendpoint (TE=0).
	ciliumHostDevice = "cilium_host"
)

// SelectHostIP chooses a VXLAN tunnelendpoint HostIP for ClusterMesh-shaped ipcache
// entries on this node. Cilium zeroes tunnelendpoint when HostIP equals the local
// node IP, so the result must never equal localIP.
//
// Preference order:
//  1. preferred when set and different from localIP (explicit override)
//  2. when this node is the gateway (localIP == gatewayIP): localOverlayIP
//     (typically cilium_host) when set and different from localIP — avoids
//     hairpin via another worker for GW-local pods
//  3. gatewayIP when set and different from localIP
//  4. first numerically sorted candidate from nodeIPs that differs from localIP
//
// Callers should pass Ready, non-control-plane node IPs in nodeIPs when possible.
func SelectHostIP(localIP, gatewayIP, preferred string, nodeIPs []string, localOverlayIP string) (string, error) {
	local := normalizeIPv4(localIP)
	if local == "" {
		return "", errors.New("local node IP is empty")
	}

	if p := normalizeIPv4(preferred); p != "" && p != local {
		return p, nil
	}

	gw := normalizeIPv4(gatewayIP)
	if gw != "" && gw == local {
		if o := normalizeIPv4(localOverlayIP); o != "" && o != local {
			return o, nil
		}
	}

	if gw != "" && gw != local {
		return gw, nil
	}

	candidates := make([]string, 0, len(nodeIPs))

	for _, raw := range nodeIPs {
		ip := normalizeIPv4(raw)
		if ip == "" || ip == local {
			continue
		}

		candidates = append(candidates, ip)
	}

	slices.SortFunc(candidates, compareIPv4)
	candidates = slices.Compact(candidates)

	if len(candidates) == 0 {
		return "", fmt.Errorf("no HostIP candidate distinct from local IP %s (need ≥2 worker nodes, a gateway IP, or cilium_host overlay)", local)
	}

	return candidates[0], nil
}

// CiliumHostIPv4 returns the IPv4 address on the cilium_host device, if present.
func CiliumHostIPv4(netLink netlink.Interface) (string, error) {
	if netLink == nil {
		netLink = netlink.New()
	}

	link, err := netLink.LinkByName(ciliumHostDevice)
	if err != nil {
		return "", errors.Wrapf(err, "link %s", ciliumHostDevice)
	}

	addrs, err := netLink.AddrList(link, k8snet.IPv4)
	if err != nil {
		return "", errors.Wrapf(err, "list addresses on %s", ciliumHostDevice)
	}

	for i := range addrs {
		ip := addrs[i].IP
		if ip == nil || ip.IsUnspecified() || ip.IsLoopback() {
			continue
		}

		if v4 := normalizeIPv4(ip.String()); v4 != "" {
			return v4, nil
		}
	}

	return "", fmt.Errorf("no IPv4 address on %s", ciliumHostDevice)
}

// InternalIPv4 returns the first IPv4 NodeInternalIP on the node, if any.
func InternalIPv4(node *corev1.Node) string {
	if node == nil {
		return ""
	}

	for i := range node.Status.Addresses {
		addr := node.Status.Addresses[i]
		if addr.Type != corev1.NodeInternalIP {
			continue
		}

		if k8snet.IPFamilyOfString(addr.Address) == k8snet.IPv4 {
			return normalizeIPv4(addr.Address)
		}
	}

	return ""
}

func isNodeReady(node *corev1.Node) bool {
	for i := range node.Status.Conditions {
		c := &node.Status.Conditions[i]
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}

	return false
}

func isControlPlaneNode(node *corev1.Node) bool {
	if node == nil || node.Labels == nil {
		return false
	}

	_, cp := node.Labels[labelControlPlane]
	_, master := node.Labels[labelMaster]

	return cp || master
}

// hostIPCandidateIPs returns InternalIPs of Ready non-control-plane nodes.
func hostIPCandidateIPs(nodes []corev1.Node) []string {
	ips := make([]string, 0, len(nodes))

	for i := range nodes {
		node := &nodes[i]
		if !isNodeReady(node) || isControlPlaneNode(node) {
			continue
		}

		if ip := InternalIPv4(node); ip != "" {
			ips = append(ips, ip)
		}
	}

	return ips
}

func compareIPv4(a, b string) int {
	aa, errA := netip.ParseAddr(a)
	bb, errB := netip.ParseAddr(b)

	if errA != nil || errB != nil {
		return strings.Compare(a, b)
	}

	return aa.Compare(bb)
}

func normalizeIPv4(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	ip := net.ParseIP(s)
	if ip == nil {
		return ""
	}

	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}

	return ""
}
