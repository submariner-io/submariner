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
	"strconv"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	fakeNetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	"github.com/vishvananda/netlink"
)

var _ = Describe("Function getVxlanVtepIPAddress", func() {
	When("a valid IP address is provided", func() {
		It("should return the VxLAN VtepIP that can be configured", func() {
			vtepIP, _ := getVxlanVtepIPAddress("192.168.100.24")
			Expect(vtepIP.String()).Should(Equal(strconv.Itoa(VxLANVTepNetworkPrefix) + ".168.100.24"))
		})
	})

	When("an invalid IP address is provided", func() {
		It("should return an error", func() {
			_, err := getVxlanVtepIPAddress("10.0.0")
			Expect(err).ShouldNot(Equal(nil))
		})
	})
})

var _ = Describe("Function createVxLanIface", func() {
	var (
		netLink *fakeNetlink.NetLink
		handler *SyncHandler
		iface   *vxLanIface
	)

	BeforeEach(func() {
		netLink = fakeNetlink.New()
		iface = &vxLanIface{
			netLink: netLink,
			link: &netlink.Vxlan{
				LinkAttrs: netlink.LinkAttrs{
					Name: VxLANIface,
				},
				VxlanId: 100,
				Group:   net.ParseIP("192.68.1.2"),
				Port:    VxLANPort,
			},
		}

		handler = &SyncHandler{
			netLink: netLink,
		}
	})

	It("should create the interface", func() {
		Expect(handler.createVxLanIface(iface)).To(Succeed())
		Expect(netLink.LinkByName(iface.link.Name)).To(Equal(iface.link))
	})

	When("the VxLAN interface already exists", func() {
		var existing netlink.Vxlan

		BeforeEach(func() {
			existing = *iface.link
		})

		JustBeforeEach(func() {
			Expect(netLink.LinkAdd(&existing)).To(Succeed())
			Expect(handler.createVxLanIface(iface)).To(Succeed())
		})

		Context("and the new interface matches", func() {
			It("should succeed", func() {
			})
		})

		Context("and the new interface does not match", func() {
			BeforeEach(func() {
				existing.Group = net.ParseIP("192.68.1.3")
			})

			It("should recreate the interface ", func() {
				Expect(netLink.LinkByName(iface.link.Name)).To(Equal(iface.link))
			})
		})
	})
})
