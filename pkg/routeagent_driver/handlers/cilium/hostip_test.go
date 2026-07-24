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

package cilium //nolint:testpackage // Tests exercise unexported HostIP selection helpers.

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("SelectHostIP", func() {
	const local = "10.0.0.1"

	It("should prefer an explicit preferred IP when distinct from local", func() {
		ip, err := SelectHostIP(local, "10.0.0.2", "10.0.0.9", []string{local, "10.0.0.3"}, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ip).To(Equal("10.0.0.9"))
	})

	It("should prefer the gateway IP when distinct from local", func() {
		ip, err := SelectHostIP(local, "10.0.0.2", "", []string{local, "10.0.0.3"}, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ip).To(Equal("10.0.0.2"))
	})

	It("should ignore preferred and gateway when they equal local", func() {
		ip, err := SelectHostIP(local, local, local, []string{local, "10.0.0.3", "10.0.0.2"}, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ip).To(Equal("10.0.0.2")) // numeric sort, not lexical
	})

	It("should sort candidates numerically so 10.0.0.2 precedes 10.0.0.10", func() {
		ip, err := SelectHostIP(local, local, "", []string{local, "10.0.0.10", "10.0.0.2"}, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ip).To(Equal("10.0.0.2"))
	})

	It("should pick the lowest worker IP rather than a higher egress-like IP when on GW without overlay", func() {
		// Mirrors stage-like addresses: egress 46.224.101.91 sorts before worker
		// 46.224.67.227 lexicographically, but numerically worker is lower.
		const (
			worker = "46.224.67.227"
			egress = "46.224.101.91"
			gw     = "46.224.73.0"
		)
		ip, err := SelectHostIP(gw, gw, "", []string{gw, worker, egress}, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ip).To(Equal(worker))
	})

	It("should prefer cilium_host overlay on the gateway over hairpin to a worker", func() {
		const (
			worker  = "46.224.67.227"
			gw      = "46.224.73.0"
			overlay = "10.244.6.103"
		)
		ip, err := SelectHostIP(gw, gw, "", []string{gw, worker}, overlay)
		Expect(err).NotTo(HaveOccurred())
		Expect(ip).To(Equal(overlay))
	})

	It("should not use overlay on non-gateway nodes", func() {
		ip, err := SelectHostIP(local, "10.0.0.2", "", []string{local, "10.0.0.3"}, "10.244.1.1")
		Expect(err).NotTo(HaveOccurred())
		Expect(ip).To(Equal("10.0.0.2"))
	})

	It("should fail when no distinct candidate exists", func() {
		_, err := SelectHostIP(local, local, "", []string{local}, "")
		Expect(err).To(HaveOccurred())
	})

	It("should fail when local IP is empty", func() {
		_, err := SelectHostIP("", "10.0.0.2", "", []string{"10.0.0.2"}, "")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("hostIPCandidateIPs", func() {
	It("should skip NotReady and control-plane nodes", func() {
		nodes := []corev1.Node{
			*readyNode("cp", "10.0.0.10", map[string]string{labelControlPlane: ""}),
			*readyNode("worker", "10.0.0.2", nil),
			*notReadyNode("sick", "10.0.0.3", nil),
			*readyNode("master", "10.0.0.11", map[string]string{labelMaster: "true"}),
		}

		Expect(hostIPCandidateIPs(nodes)).To(Equal([]string{"10.0.0.2"}))
	})

	It("should extract InternalIPv4 from a Node", func() {
		node := readyNode("n1", "10.0.0.5", nil)
		Expect(InternalIPv4(node)).To(Equal("10.0.0.5"))
	})
})

func readyNode(name, ip string, labels map[string]string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: ip},
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func notReadyNode(name, ip string, labels map[string]string) *corev1.Node {
	n := readyNode(name, ip, labels)
	n.Status.Conditions[0].Status = corev1.ConditionFalse

	return n
}
