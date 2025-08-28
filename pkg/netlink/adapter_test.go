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

package netlink_test

import (
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	"github.com/submariner-io/submariner/pkg/netlink/fake"
	"github.com/vishvananda/netlink"
	k8snet "k8s.io/utils/net"
)

var _ = Describe("Adapter", func() {
	var (
		adapter *netlinkAPI.Adapter
		fakeNL  *fake.NetLink
		rule    *netlink.Rule
	)

	BeforeEach(func() {
		fakeNL = fake.New()
		adapter = &netlinkAPI.Adapter{Basic: fakeNL.Basic}

		rule = netlinkAPI.NewTableRule(100, k8snet.IPv4)
	})

	Describe("RuleAddIfNotPresent", func() {
		Context("when the rule does not exist", func() {
			It("should add it successfully", func() {
				Expect(adapter.RuleAddIfNotPresent(rule)).To(Succeed())
				fakeNL.AwaitRule(rule.Table, "", "")
			})
		})

		Context("when the rule already exists", func() {
			BeforeEach(func() {
				Expect(fakeNL.RuleAdd(rule)).To(Succeed())
			})

			It("should not return an error", func() {
				Expect(adapter.RuleAddIfNotPresent(rule)).To(Succeed())
			})
		})

		Context("when RuleAdd fails", func() {
			BeforeEach(func() {
				_, rule.Src, _ = net.ParseCIDR("10.253.0.0/16")
				_, rule.Dst, _ = net.ParseCIDR("2001:0:0:1234::/64")
			})

			It("should return the error", func() {
				Expect(adapter.RuleAddIfNotPresent(rule)).NotTo(Succeed())
			})
		})
	})

	Describe("RuleDelIfPresent", func() {
		Context("when the rule exists", func() {
			BeforeEach(func() {
				Expect(fakeNL.RuleAdd(rule)).To(Succeed())
			})

			It("should delete it successfully", func() {
				Expect(adapter.RuleDelIfPresent(rule)).To(Succeed())
				fakeNL.AwaitNoRule(rule.Table, "", "")
			})
		})

		Context("when the rule does not exist", func() {
			It("should not return an error", func() {
				Expect(adapter.RuleDelIfPresent(rule)).To(Succeed())
			})
		})
	})

	Describe("RouteAddOrReplace", func() {
		var route *netlink.Route

		BeforeEach(func() {
			_, destNet, _ := net.ParseCIDR("192.168.1.0/24")
			route = &netlink.Route{
				LinkIndex: 1,
				Dst:       destNet,
				Gw:        net.ParseIP("192.168.1.1"),
				Table:     100,
			}
		})

		Context("when the route does not exist", func() {
			It("should add it successfully", func() {
				Expect(adapter.RouteAddOrReplace(route)).To(Succeed())
				fakeNL.AwaitDstRoutes(route.LinkIndex, route.Table, route.Dst.String())
			})
		})

		Context("when the route already exists", func() {
			BeforeEach(func() {
				Expect(fakeNL.RouteAdd(route)).To(Succeed())
			})

			It("should replace it successfully", func() {
				route.Priority = 200

				Expect(adapter.RouteAddOrReplace(route)).To(Succeed())

				routes, _ := fakeNL.RouteList(&netlink.GenericLink{
					LinkAttrs: netlink.LinkAttrs{Index: route.LinkIndex},
				}, k8snet.IPv4)
				Expect(routes).To(ContainElement(HaveField("Priority", 200)))
			})
		})
	})

	Describe("GetDefaultGatewayInterface", func() {
		intf := net.Interface{Name: "eth0", Index: 99}

		Context("when the default gateway route exists", func() {
			BeforeEach(func() {
				fakeNL.SetupDefaultGateway(k8snet.IPv4, intf)
			})

			It("should return the default gateway interface", func() {
				iface, err := adapter.GetDefaultGatewayInterface(k8snet.IPv4)
				Expect(err).NotTo(HaveOccurred())
				Expect(iface.Index()).To(Equal(intf.Index))
			})
		})

		Context("when no default gateway route exists", func() {
			It("should return an error", func() {
				_, err := adapter.GetDefaultGatewayInterface(k8snet.IPv4)
				Expect(err).To(HaveOccurred())
			})
		})
	})
})
