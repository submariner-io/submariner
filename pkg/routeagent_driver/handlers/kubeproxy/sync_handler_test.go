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
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/kubeproxy"
	corev1 "k8s.io/api/core/v1"
	k8snet "k8s.io/utils/net"
)

var _ = Describe("SyncHandler", func() {
	Describe("Endpoints", testEndpoints)
	Describe("Gateway transition", testGatewayTransition)
	Describe("Nodes", testNodes)
	Describe("Uninstall", testUninstall)
	Describe("Dual-stack", testDualStack)
})

func testNodes() {
	t := newTestDriver()

	var node *corev1.Node

	BeforeEach(func() {
		node = newNode(nodeIPv4Address1)
	})

	When("a Node is created and then deleted on a gateway node", func() {
		JustBeforeEach(func() {
			t.CreateLocalHostEndpoint()
			t.CreateNode(node)
		})

		It("should add/remove an FDB entry on the VxLAN interface for each Node address", func() {
			t.netLink.AwaitNeighbors(vxLanInterfaceIndex, nodeIPv4Address1)

			t.DeleteNode(node.Name)
			t.netLink.AwaitNoNeighbors(vxLanInterfaceIndex, nodeIPv4Address1)
		})
	})

	When("a Node is created on a non-gateway node", func() {
		JustBeforeEach(func() {
			t.CreateNode(node)
		})

		It("should not add an FDB entry on the VxLAN interface for each Node address", func() {
			t.netLink.AwaitNoNeighbors(vxLanInterfaceIndex, nodeIPv4Address1)
		})
	})
}

func testUninstall() {
	t := newTestDriver()

	Context("on Uninstall", func() {
		It("should clean up dataplane artifacts", func() {
			t.CreateLocalHostEndpoint()
			t.netLink.AwaitRule(constants.RouteAgentHostNetworkTableID, "", "")

			Expect(t.handler.Uninstall()).To(Succeed())

			t.netLink.AwaitNoRule(constants.RouteAgentHostNetworkTableID, "", "")
			t.netLink.AwaitNoLink(kubeproxy.GetVxLANInterfaceName(k8snet.IPv4))
			t.verifyNoHostNetworkingRoutes()
		})
	})
}
