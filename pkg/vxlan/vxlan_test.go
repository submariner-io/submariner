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

package vxlan_test

import (
	"net"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	fakeNetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	"github.com/submariner-io/submariner/pkg/vxlan"
	"github.com/vishvananda/netlink"
)

func TestVxlan(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Vxlan Suite")
}

var _ = Describe("NewInterface", func() {
	t := newTestDriver()

	When("the vxlan link device doesn't exist", func() {
		It("should create it", func() {
			_ = t.newInterface(t.vxlanAttrs)
			t.assertLink(t.vxlanAttrs)
		})
	})

	When("the vxlan link device already exists", func() {
		var newAttrs vxlan.Attributes

		BeforeEach(func() {
			_ = t.newInterface(t.vxlanAttrs)
			t.assertLink(t.vxlanAttrs)
			newAttrs = *t.vxlanAttrs
		})

		Context("and the configuration is the same", func() {
			It("shouldn't re-create it", func() {
				newAttrs.Mtu = 200
				_ = t.newInterface(&newAttrs)
				t.assertLink(t.vxlanAttrs)
			})
		})

		Context("and the Group differs", func() {
			It("should re-create it", func() {
				newAttrs.Group = net.ParseIP("11.22.33.44")
				_ = t.newInterface(&newAttrs)
				t.assertLink(&newAttrs)
			})
		})

		Context("and the VxlanId differs", func() {
			It("should re-create it", func() {
				newAttrs.VxlanID = t.vxlanAttrs.VxlanID + 1
				_ = t.newInterface(&newAttrs)
				t.assertLink(&newAttrs)
			})
		})

		Context("and the SrcAddr differs", func() {
			It("should re-create it", func() {
				newAttrs.SrcAddr = net.ParseIP("11.22.33.44")
				_ = t.newInterface(&newAttrs)
				t.assertLink(&newAttrs)
			})
		})

		Context("and the Port differs", func() {
			It("should re-create it", func() {
				newAttrs.VtepPort = t.vxlanAttrs.VtepPort + 1
				_ = t.newInterface(&newAttrs)
				t.assertLink(&newAttrs)
			})
		})
	})
})

var _ = Describe("GetVtepIPAddressFrom", func() {
	It("should return the correct IP", func() {
		vtepIP, err := vxlan.GetVtepIPAddressFrom("10.17.2.3", 240)
		Expect(err).To(Succeed())
		Expect(vtepIP).To(Equal(net.ParseIP("240.17.2.3")))
	})

	Specify("should return an error if the input IP is invalid", func() {
		_, err := vxlan.GetVtepIPAddressFrom("10.17.2", 240)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Interface", func() {
	t := newTestDriver()

	var vxlanInterface *vxlan.Interface

	BeforeEach(func() {
		vxlanInterface = t.newInterface(t.vxlanAttrs)
	})

	Specify("ConfigureIPAddress should add the IP to the link device", func() {
		addrCh := make(chan netlink.AddrUpdate, 100)
		_ = t.netLink.AddrSubscribe(addrCh, nil)

		ip := net.ParseIP("240.17.2.3")
		mask := net.CIDRMask(8, 32)
		Expect(vxlanInterface.ConfigureIPAddress(ip, mask)).To(Succeed())

		Eventually(addrCh).Should(Receive(&netlink.AddrUpdate{
			LinkAddress: net.IPNet{
				IP:   ip,
				Mask: mask,
			},
			NewAddr: true,
		}))

		Expect(vxlanInterface.ConfigureIPAddress(ip, mask)).To(Succeed())
		Consistently(addrCh).ShouldNot(Receive())
	})

	Specify("DeleteLinkDevice should delete the link device", func() {
		Expect(vxlanInterface.DeleteLinkDevice()).To(Succeed())
		t.netLink.AwaitNoLink(t.vxlanAttrs.Name)
	})

	Specify("SetupLink should invoke LinkSetup", func() {
		Expect(vxlanInterface.SetupLink()).To(Succeed())
		t.netLink.AwaitLinkSetup(t.vxlanAttrs.Name)
	})

	Specify("AddFDB and DelFDB should add/remove an FDB entry", func() {
		ip := net.ParseIP("120.17.2.3")
		Expect(vxlanInterface.AddFDB(ip, "00:00:00:00:00:00")).To(Succeed())
		t.netLink.AwaitNeighbors(0, ip.String())

		Expect(vxlanInterface.DelFDB(ip, "00:00:00:00:00:00")).To(Succeed())
		t.netLink.AwaitNoNeighbors(0, ip.String())
	})

	Specify("AddRoutes and DelRoutes should add/remove routes", func() {
		destCIDR1 := "10.26.0.0/16"
		destCIDR2 := "11.26.0.0/16"

		_, ipNet1, err := net.ParseCIDR(destCIDR1)
		Expect(err).To(Succeed())

		_, ipNet2, err := net.ParseCIDR(destCIDR2)
		Expect(err).To(Succeed())

		Expect(vxlanInterface.AddRoutes(net.ParseIP("240.17.2.3"), net.ParseIP("120.17.2.3"), 100, *ipNet1, *ipNet2)).To(Succeed())
		t.netLink.AwaitDstRoutes(0, 100, destCIDR1, destCIDR2)

		Expect(vxlanInterface.AddRoutes(net.ParseIP("240.17.2.3"), net.ParseIP("120.17.2.3"), 100, *ipNet1, *ipNet2)).To(Succeed())
		list, err := t.netLink.RouteList(&netlink.GenericLink{}, 0)
		Expect(err).To(Succeed())
		Expect(list).To(HaveLen(2))

		Expect(vxlanInterface.DelRoutes(100, *ipNet1, *ipNet2)).To(Succeed())
		t.netLink.AwaitNoDstRoutes(0, 100, destCIDR1, destCIDR2)
	})
})

type testDriver struct {
	netLink    *fakeNetlink.NetLink
	vxlanAttrs *vxlan.Attributes
}

func newTestDriver() *testDriver {
	t := &testDriver{}

	BeforeEach(func() {
		t.netLink = fakeNetlink.New()

		t.vxlanAttrs = &vxlan.Attributes{
			Name:     "vx-submariner",
			VxlanID:  100,
			Group:    net.ParseIP("10.17.2.3"),
			VtepPort: 4800,
			Mtu:      100,
		}
	})

	return t
}

func (t *testDriver) newInterface(attrs *vxlan.Attributes) *vxlan.Interface {
	iface, err := vxlan.NewInterface(attrs, t.netLink)
	Expect(err).To(Succeed())
	Expect(iface).ToNot(BeNil())

	return iface
}

func (t *testDriver) assertLink(attrs *vxlan.Attributes) {
	link := t.netLink.AwaitLink(attrs.Name).(*netlink.Vxlan)
	Expect(link.VxlanId).To(Equal(attrs.VxlanID))
	Expect(link.SrcAddr).To(Equal(attrs.SrcAddr))
	Expect(link.Group).To(Equal(attrs.Group))
	Expect(link.Port).To(Equal(attrs.VtepPort))
	Expect(link.MTU).To(Equal(attrs.Mtu - vxlan.MTUOverhead))
}
