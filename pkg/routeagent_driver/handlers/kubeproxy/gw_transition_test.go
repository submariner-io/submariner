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
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
)

func testGatewayTransition() {
	t := newTestDriver()

	When("transition to gateway", func() {
		var localHostEP *submarinerv1.Endpoint

		JustBeforeEach(func() {
			t.CreateEndpoint(t.remoteEndpoint)
			localHostEP = t.CreateLocalHostEndpoint()
		})

		It("should add the VxLAN interface", func() {
			Expect(t.awaitVxlanLink().Group).To(BeNil())
		})

		It("should add a routing rule for the RouteAgentHostNetworkTableID", func() {
			t.netLink.AwaitRule(constants.RouteAgentHostNetworkTableID, "", "")
		})

		It("should add host networking routing rules for the remote subnets", func() {
			t.verifyHostNetworkingRoutes()
		})

		Context("and previous VxLAN routes are present", func() {
			BeforeEach(func() {
				t.addVxLANRoute(remoteIPv4Subnet1)
			})

			It("should remove them", func() {
				t.netLink.AwaitNoDstRoutes(vxLanInterfaceIndex, 0, remoteIPv4Subnet1)
			})
		})

		Context("and Node addresses are present", func() {
			JustBeforeEach(func() {
				t.CreateNode(newNode(nodeIPv4Address1))
				t.CreateNode(newNode(nodeIPv4Address2))
			})

			It("should add an FDB entry on the VxLAN interface for each address", func() {
				t.netLink.AwaitNeighbors(vxLanInterfaceIndex, nodeIPv4Address1, nodeIPv4Address2)
			})
		})

		Context("and then to non-gateway", func() {
			JustBeforeEach(func() {
				t.netLink.AwaitRule(constants.RouteAgentHostNetworkTableID, "", "")
				t.DeleteEndpoint(localHostEP.Name)
			})

			It("should remove the routing rule for the RouteAgentHostNetworkTableID", func() {
				t.netLink.AwaitNoRule(constants.RouteAgentHostNetworkTableID, "", "")
			})

			It("should remove host networking routing rules for the remote subnets", func() {
				t.verifyNoHostNetworkingRoutes()
			})
		})
	})
}
