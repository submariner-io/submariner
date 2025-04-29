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

package kubeproxy_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8snet "k8s.io/utils/net"
)

func testDualStack() {
	nodeAddresses := map[k8snet.IPFamily]string{k8snet.IPv4: nodeIPv4Address1, k8snet.IPv6: nodeIPv6Address}

	t := newTestDriver()

	BeforeEach(func() {
		t.remoteEndpoint.Spec.Subnets = []string{remoteIPv6Subnet, remoteIPv4Subnet1}
		t.remoteEndpoint.Spec.PrivateIPs = append(t.remoteEndpoint.Spec.PrivateIPs, "4000:0:0:1234::")
		t.localEndpoint.Spec.PrivateIPs = append(t.localEndpoint.Spec.PrivateIPs, "5000:0:0:1234::")
		t.localClusterCIDRs = []string{localClusterIPv4CIDR, localClusterIPv6CIDR}
		t.localServiceCIDRs = []string{localServiceIPv4CIDR, localServiceIPv6CIDR}
	})

	testIPFamily := func() {
		When("a dual-stack remote Endpoint is created while on a gateway node", func() {
			JustBeforeEach(func() {
				t.CreateLocalHostEndpoint()
				t.CreateEndpoint(t.remoteEndpoint)
			})

			It("should only add rules for the target IP family", func() {
				t.verifyHostNetworkingRoutes()
				t.verifyRemoteSubnetIPTableRules()
			})
		})

		When("a dual-stack remote Endpoint is created while on a non-gateway node", func() {
			JustBeforeEach(func() {
				t.CreateEndpoint(t.localEndpoint)
				t.CreateEndpoint(t.remoteEndpoint)
			})

			It("should only add VxLAN routes for the target IP family", func() {
				Expect(t.awaitVxlanLink().Group.String()).To(Equal(t.localEndpoint.Spec.GetPrivateIP(t.ipFamily)))
				t.verifyVxLANRoutes()
			})
		})

		When("a Node with dual-stack addresses is created on a gateway node", func() {
			JustBeforeEach(func() {
				t.CreateLocalHostEndpoint()

				for _, addr := range nodeAddresses {
					t.CreateNode(newNode(addr))
				}
			})

			It("should only add an FDB entry on the VxLAN interface for the target IP family address", func() {
				t.netLink.AwaitNeighbors(t.getVxLanInterfaceIndex(), nodeAddresses[t.ipFamily])
			})
		})
	}

	Context("IPv4", func() {
		BeforeEach(func() {
			t.ipFamily = k8snet.IPv4
		})

		testIPFamily()
	})

	Context("IPv6", func() {
		BeforeEach(func() {
			t.ipFamily = k8snet.IPv6
			t.hostInterfaceAddr = "3000:0:0:1234::/64"
		})

		testIPFamily()
	})
}
