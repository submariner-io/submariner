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

package awsvpc

import (
	"net"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	"github.com/submariner-io/submariner/pkg/vxlan"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	corev1 "k8s.io/api/core/v1"
	k8snet "k8s.io/utils/net"
	"k8s.io/utils/set"
)

func (h *Handler) onPod(pod *corev1.Pod, deleted bool) {
	if !h.enabled || !h.State().IsOnGateway() {
		return
	}

	if pod.Spec.NodeName == "" || pod.Spec.NodeName == h.nodeName {
		return
	}

	// hostNetwork pods use the node InternalIP; covered by node ingress routes.
	if pod.Spec.HostNetwork {
		return
	}

	key := podKey(pod)

	if deleted {
		ip := h.takeTrackedPodIP(key)
		if ip == "" {
			ip = podIPv4(pod)
		}

		if ip != "" {
			h.removeIngressRouteByIPString(ip)
		}

		return
	}

	podIP := podIPv4(pod)
	if podIP == "" {
		return
	}

	if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		h.clearTrackedPodIP(key)
		h.removeIngressRouteByIPString(podIP)

		return
	}

	if pod.Status.Phase != corev1.PodRunning && pod.Status.PodIP == "" {
		return
	}

	h.trackPodIP(key, podIP)
	h.ensurePodIngressRoute(pod.Spec.NodeName, podIP)
}

func podKey(pod *corev1.Pod) string {
	return pod.Namespace + "/" + pod.Name
}

func (h *Handler) trackPodIP(key, ip string) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	if prev, ok := h.podIPs[key]; ok && prev != "" && prev != ip {
		h.removePodIngressRouteLocked(prev + "/32")
	}

	h.podIPs[key] = ip
}

func (h *Handler) takeTrackedPodIP(key string) string {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	ip := h.podIPs[key]
	delete(h.podIPs, key)

	return ip
}

func (h *Handler) clearTrackedPodIP(key string) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	delete(h.podIPs, key)
}

func (h *Handler) reconcileIngressRoutes() {
	if !h.State().IsOnGateway() {
		return
	}

	// Re-apply tracked routes (e.g. after vx-submariner recreate).
	h.mutex.Lock()
	defer h.mutex.Unlock()

	for dstCIDR, vtep := range h.ingressRoutes {
		if err := h.addHostRouteLocked(dstCIDR, vtep, 0); err != nil {
			logger.Errorf(err, "Failed to re-apply pod ingress route %s via %s", dstCIDR, vtep)
		}
	}

	for dstCIDR, vtep := range h.nodeIngressRoutes {
		// Ensure leftovers in main (from older builds) are removed.
		_ = h.delHostRouteLocked(dstCIDR, 0)

		if err := h.addHostRouteLocked(dstCIDR, vtep, awsVPCNodeIngressTableID); err != nil {
			logger.Errorf(err, "Failed to re-apply node ingress route %s via %s", dstCIDR, vtep)
		}
	}

	h.syncNodeIngressPolicyLocked()
}

// ensurePodIngressRoute programs dstIP/32 via the node's VTEP in the main table.
func (h *Handler) ensurePodIngressRoute(nodeName, dstIP string) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	h.ensurePodIngressRouteLocked(nodeName, dstIP)
}

func (h *Handler) ensurePodIngressRouteLocked(nodeName, dstIP string) {
	vtepStr, ok := h.vtepForNodeLocked(nodeName, dstIP)
	if !ok {
		return
	}

	dstCIDR := dstIP + "/32"

	if existing, ok := h.ingressRoutes[dstCIDR]; ok && existing == vtepStr {
		return
	}

	if err := h.addHostRouteLocked(dstCIDR, vtepStr, 0); err != nil {
		logger.Errorf(err, "Failed to add pod ingress route %s via %s", dstCIDR, vtepStr)
		return
	}

	h.ingressRoutes[dstCIDR] = vtepStr
	logger.Infof("Programmed AWS VPC CNI pod ingress route %s via %s (node %s)", dstCIDR, vtepStr, nodeName)
}

func (h *Handler) ensureNodeIngressRouteLocked(nodeName, dstIP string) {
	vtepStr, ok := h.vtepForNodeLocked(nodeName, dstIP)
	if !ok {
		return
	}

	dstCIDR := dstIP + "/32"

	if existing, ok := h.nodeIngressRoutes[dstCIDR]; ok && existing == vtepStr {
		return
	}

	// Node IPs must not live in main — VXLAN FDB uses them as underlay destinations.
	_ = h.delHostRouteLocked(dstCIDR, 0)

	if err := h.addHostRouteLocked(dstCIDR, vtepStr, awsVPCNodeIngressTableID); err != nil {
		logger.Errorf(err, "Failed to add node ingress route %s via %s", dstCIDR, vtepStr)
		return
	}

	h.nodeIngressRoutes[dstCIDR] = vtepStr
	logger.Infof("Programmed AWS VPC CNI node ingress route %s via %s table %d (node %s)",
		dstCIDR, vtepStr, awsVPCNodeIngressTableID, nodeName)
}

func (h *Handler) vtepForNodeLocked(nodeName, dstIP string) (string, bool) {
	if nodeName == h.nodeName {
		return "", false
	}

	nodeIP, ok := h.nodeIPs[nodeName]
	if !ok || nodeIP == "" {
		logger.V(log.DEBUG).Infof("No InternalIP yet for node %q (dst %s); will retry", nodeName, dstIP)
		return "", false
	}

	vtepIP, err := vxlan.GetVtepIPAddressFrom(nodeIP, vtepPrefixCIDR, k8snet.IPv4)
	if err != nil {
		logger.Errorf(err, "Failed to derive VTEP for node %s (%s)", nodeName, nodeIP)
		return "", false
	}

	return vtepIP.String(), true
}

func (h *Handler) programAllNodeIngressRoutesLocked() {
	for nodeName, nodeIP := range h.nodeIPs {
		h.ensureNodeIngressRouteLocked(nodeName, nodeIP)
	}
}

func (h *Handler) removeIngressRouteByIPString(dstIP string) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	h.removePodIngressRouteLocked(dstIP + "/32")
}

func (h *Handler) removePodIngressRouteLocked(dstCIDR string) {
	if _, ok := h.ingressRoutes[dstCIDR]; !ok {
		return
	}

	if err := h.delHostRouteLocked(dstCIDR, 0); err != nil {
		logger.Errorf(err, "Failed to delete pod ingress route %s", dstCIDR)
	}

	delete(h.ingressRoutes, dstCIDR)
	logger.Infof("Removed AWS VPC CNI pod ingress route %s", dstCIDR)
}

func (h *Handler) removeNodeIngressRouteLocked(dstCIDR string) {
	if _, ok := h.nodeIngressRoutes[dstCIDR]; !ok {
		return
	}

	if err := h.delHostRouteLocked(dstCIDR, awsVPCNodeIngressTableID); err != nil {
		logger.Errorf(err, "Failed to delete node ingress route %s", dstCIDR)
	}

	_ = h.delHostRouteLocked(dstCIDR, 0)

	delete(h.nodeIngressRoutes, dstCIDR)
	logger.Infof("Removed AWS VPC CNI node ingress route %s", dstCIDR)
}

func (h *Handler) clearIngressRoutesLocked() {
	for dstCIDR := range h.ingressRoutes {
		if err := h.delHostRouteLocked(dstCIDR, 0); err != nil {
			logger.Errorf(err, "Failed to delete pod ingress route %s", dstCIDR)
		}
	}

	for dstCIDR := range h.nodeIngressRoutes {
		if err := h.delHostRouteLocked(dstCIDR, awsVPCNodeIngressTableID); err != nil {
			logger.Errorf(err, "Failed to delete node ingress route %s", dstCIDR)
		}

		_ = h.delHostRouteLocked(dstCIDR, 0)
	}

	h.ingressRoutes = map[string]string{}
	h.nodeIngressRoutes = map[string]string{}
	h.podIPs = map[string]string{}
	h.clearNodeIngressPolicyLocked()
}

// syncNodeIngressPolicyLocked installs PBR so node /32 VTEP routes are used for
// return traffic from the cable without hijacking VXLAN underlay lookups in main.
func (h *Handler) syncNodeIngressPolicyLocked() {
	if !h.State().IsOnGateway() {
		h.clearNodeIngressPolicyLocked()
		return
	}

	desiredSrc := h.remoteCIDRs.Clone()

	existing, err := h.netLink.RuleList(k8snet.IPv4)
	if err != nil {
		logger.Errorf(err, "Failed to list ip rules for node ingress policy")
		return
	}

	haveCableRule := h.reconcileNodeIngressRulesLocked(existing, desiredSrc)
	h.ensureNodeIngressSrcRulesLocked(desiredSrc)

	if h.cableIfaceName != "" && !haveCableRule {
		rule := netlinkAPI.NewTableRule(awsVPCNodeIngressTableID, k8snet.IPv4)
		rule.IifName = h.cableIfaceName

		if err := h.netLink.RuleAddIfNotPresent(rule); err != nil {
			logger.Errorf(err, "Failed to add node ingress rule iif %s", h.cableIfaceName)
		} else {
			logger.Infof("Added AWS VPC CNI node ingress rule iif %s lookup %d",
				h.cableIfaceName, awsVPCNodeIngressTableID)
		}
	}
}

// reconcileNodeIngressRulesLocked drops stale table rules and returns whether the
// cable iif rule is already present. desiredSrc is updated in place for CIDRs that
// already have a matching src rule.
func (h *Handler) reconcileNodeIngressRulesLocked(
	existing []netlink.Rule, desiredSrc set.Set[string],
) bool {
	haveCableRule := false

	for i := range existing {
		rule := existing[i]
		if rule.Table != awsVPCNodeIngressTableID {
			continue
		}

		if rule.IifName != "" {
			if h.cableIfaceName != "" && rule.IifName == h.cableIfaceName {
				haveCableRule = true
				continue
			}

			_ = h.netLink.RuleDelIfPresent(&rule)

			continue
		}

		if rule.Src == nil {
			_ = h.netLink.RuleDelIfPresent(&rule)
			continue
		}

		src := rule.Src.String()
		if desiredSrc.Has(src) {
			desiredSrc.Delete(src)
			continue
		}

		_ = h.netLink.RuleDelIfPresent(&rule)
	}

	return haveCableRule
}

func (h *Handler) ensureNodeIngressSrcRulesLocked(desiredSrc set.Set[string]) {
	for _, cidrStr := range desiredSrc.UnsortedList() {
		_, src, err := net.ParseCIDR(cidrStr)
		if err != nil {
			continue
		}

		rule := netlinkAPI.NewTableRule(awsVPCNodeIngressTableID, k8snet.IPv4)
		rule.Src = src

		if err := h.netLink.RuleAddIfNotPresent(rule); err != nil {
			logger.Errorf(err, "Failed to add node ingress rule from %s", cidrStr)
		} else {
			logger.Infof("Added AWS VPC CNI node ingress rule from %s lookup %d", cidrStr, awsVPCNodeIngressTableID)
		}
	}
}

func (h *Handler) clearNodeIngressPolicyLocked() {
	rules, err := h.netLink.RuleList(k8snet.IPv4)
	if err != nil {
		logger.Errorf(err, "Failed to list ip rules while clearing node ingress policy")
		return
	}

	for i := range rules {
		if rules[i].Table != awsVPCNodeIngressTableID {
			continue
		}

		if err := h.netLink.RuleDelIfPresent(&rules[i]); err != nil {
			logger.Errorf(err, "Failed to delete node ingress rule %#v", rules[i])
		}
	}
}

func (h *Handler) addHostRouteLocked(dstCIDR, via string, table int) error {
	link, err := h.netLink.LinkByName(vxlanIfaceName)
	if err != nil {
		return errors.Wrapf(err, "link %s not found", vxlanIfaceName)
	}

	_, dst, err := net.ParseCIDR(dstCIDR)
	if err != nil {
		return errors.Wrapf(err, "parse dest %s", dstCIDR)
	}

	gw := net.ParseIP(via)
	if gw == nil {
		return errors.Errorf("invalid gateway IP %q", via)
	}

	route := &netlink.Route{
		Dst:       dst,
		Gw:        gw,
		LinkIndex: link.Attrs().Index,
		Scope:     unix.RT_SCOPE_UNIVERSE,
		Protocol:  routeProtocol,
		Table:     table,
	}

	return errors.Wrap(h.netLink.RouteAddOrReplace(route), "RouteAddOrReplace")
}

func (h *Handler) delHostRouteLocked(dstCIDR string, table int) error {
	link, err := h.netLink.LinkByName(vxlanIfaceName)
	if err != nil {
		return errors.Wrapf(err, "link %s not found", vxlanIfaceName)
	}

	_, dst, err := net.ParseCIDR(dstCIDR)
	if err != nil {
		return errors.Wrapf(err, "parse dest %s", dstCIDR)
	}

	route := &netlink.Route{
		Dst:       dst,
		LinkIndex: link.Attrs().Index,
		Protocol:  routeProtocol,
		Table:     table,
	}

	err = h.netLink.RouteDel(route)
	if err == nil {
		return nil
	}

	// Fall back: match by dst (+ table) only (gw may differ).
	routes, listErr := h.netLink.RouteList(link, k8snet.IPv4)
	if listErr != nil {
		return errors.Wrap(err, "RouteDel")
	}

	for i := range routes {
		if routes[i].Dst == nil || routes[i].Dst.String() != dst.String() {
			continue
		}

		if routes[i].Table != table {
			continue
		}

		if delErr := h.netLink.RouteDel(&routes[i]); delErr != nil {
			return errors.Wrap(delErr, "RouteDel")
		}

		return nil
	}

	return nil
}

func podIPv4(pod *corev1.Pod) string {
	if k8snet.IPFamilyOfString(pod.Status.PodIP) == k8snet.IPv4 {
		return pod.Status.PodIP
	}

	for _, p := range pod.Status.PodIPs {
		if k8snet.IPFamilyOfString(p.IP) == k8snet.IPv4 {
			return p.IP
		}
	}

	return ""
}
