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
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/kubeproxy"
	k8snet "k8s.io/utils/net"
)

func testEndpoints() {
	t := newTestDriver()

	When("a local Endpoint is created while on a non-gateway node", func() {
		JustBeforeEach(func() {
			t.CreateEndpoint(t.localEndpoint)
		})

		It("should add the VxLAN interface", func() {
			Expect(toVxlan(t.netLink.AwaitLink(kubeproxy.VxLANIface)).Group.String()).To(Equal(
				t.localEndpoint.Spec.GetPrivateIP(k8snet.IPv4)))
		})

		Context("and old VxLAN routes are present", func() {
			BeforeEach(func() {
				t.addVxLANRoute(remoteSubnet1)
			})

			It("should remove them", func() {
				t.netLink.AwaitNoDstRoutes(t.vxLanInterfaceIndex, 0, remoteSubnet1)
			})
		})

		Context("and a VxLAN interface from a previous Endpoint exists", func() {
			JustBeforeEach(func() {
				t.netLink.AwaitLink(kubeproxy.VxLANIface)
			})

			It("should remove the previous VxLAN interface", func() {
				t.netLink.SetLinkIndex(kubeproxy.VxLANIface, t.vxLanInterfaceIndex+1)
				t.CreateEndpoint(newLocalEndpoint(localNodeName2))

				Eventually(func() int {
					return t.netLink.AwaitLink(kubeproxy.VxLANIface).Attrs().Index
				}).Should(Equal(t.vxLanInterfaceIndex + 1))
			})
		})

		Context("and remote subnets are present", func() {
			BeforeEach(func() {
				t.CreateEndpoint(t.remoteEndpoint)
			})

			It("should add VxLAN routes for the remote subnets", func() {
				t.verifyVxLANRoutes()
			})
		})
	})

	When("a local Endpoint is removed while on a non-gateway node", func() {
		BeforeEach(func() {
			t.CreateEndpoint(t.localEndpoint)
			t.netLink.AwaitLink(kubeproxy.VxLANIface)
		})

		JustBeforeEach(func() {
			t.DeleteEndpoint(t.localEndpoint.Name)
		})

		Context("and its host name matches that associated with the existing VxLAN interface", func() {
			It("should remove the existing VxLAN interface", func() {
				t.netLink.AwaitNoLink(kubeproxy.VxLANIface)
			})
		})

		Context("and its host name does not match that associated with the existing VxLAN interface", func() {
			BeforeEach(func() {
				t.localEndpoint.Spec.Hostname = localNodeName2
			})

			It("should not remove the existing VxLAN interface", func() {
				t.netLink.AwaitLink(kubeproxy.VxLANIface)
			})
		})

		Context("and is subsequently recreated", func() {
			It("should recreate the VxLAN interface", func() {
				t.netLink.AwaitNoLink(kubeproxy.VxLANIface)

				t.CreateEndpoint(t.localEndpoint)
				t.netLink.AwaitLink(kubeproxy.VxLANIface)
			})
		})
	})

	When("a local Endpoint is created while on a gateway node", func() {
		It("should not add the VxLAN interface", func() {
			t.localEndpoint.Spec.Hostname = t.Hostname
			t.CreateEndpoint(t.localEndpoint)
			t.netLink.AwaitNoLink(kubeproxy.VxLANIface)
		})
	})

	When("a remote Endpoint is created while on a non-gateway node", func() {
		JustBeforeEach(func() {
			t.CreateEndpoint(t.remoteEndpoint)
		})

		Context("after a local Endpoint was created", func() {
			BeforeEach(func() {
				t.CreateEndpoint(t.localEndpoint)
			})

			It("should add VxLAN routes for the remote subnets", func() {
				t.verifyVxLANRoutes()
			})

			It("should add IP table rules for the remote subnets", func() {
				t.verifyRemoteSubnetIPTableRules()
			})

			It("should not add routing rules for host networking", func() {
				t.verifyNoHostNetworkingRoutes()
			})

			Context("and is subsequently removed", func() {
				JustBeforeEach(func() {
					t.DeleteEndpoint(t.remoteEndpoint.Name)
				})

				It("should remove the VxLAN routes for the remote subnets", func() {
					t.verifyNoVxLANRoutes()
				})
			})
		})

		Context("before a local Endpoint is created", func() {
			It("should not add VxLAN routes for the remote subnets", func() {
				t.verifyNoVxLANRoutes()
			})

			It("should add IP table rules for the remote subnets", func() {
				t.verifyRemoteSubnetIPTableRules()
			})
		})

		Context("and is subsequently removed followed by a local Endpoint created", func() {
			JustBeforeEach(func() {
				t.DeleteEndpoint(t.remoteEndpoint.Name)
				t.CreateEndpoint(t.localEndpoint)
			})

			It("should not add VxLAN routes for the remote subnets", func() {
				t.verifyNoVxLANRoutes()
			})
		})
	})

	When("a remote Endpoint is created while on a gateway node", func() {
		JustBeforeEach(func() {
			t.CreateLocalHostEndpoint()
			t.CreateEndpoint(t.remoteEndpoint)
		})

		It("should not add VxLAN routes for the remote subnets", func() {
			t.verifyNoVxLANRoutes()
		})

		It("should add IP table rules for the remote subnets", func() {
			t.verifyRemoteSubnetIPTableRules()
		})

		It("should add routing rules for host networking", func() {
			t.verifyHostNetworkingRoutes()
		})

		Context("and is subsequently removed", func() {
			JustBeforeEach(func() {
				t.DeleteEndpoint(t.remoteEndpoint.Name)
			})

			It("should remove routing rules for host networking", func() {
				t.verifyNoHostNetworkingRoutes()
			})
		})
	})
}
