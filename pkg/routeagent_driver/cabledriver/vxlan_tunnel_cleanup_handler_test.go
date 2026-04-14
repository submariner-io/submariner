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

package cabledriver_test

import (
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	submv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable/vxlan"
	"github.com/submariner-io/submariner/pkg/event"
	eventtesting "github.com/submariner-io/submariner/pkg/event/testing"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	fakeNetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/cabledriver"
	"github.com/vishvananda/netlink"
	k8snet "k8s.io/utils/net"
)

var _ = Describe("VXLAN Cleanup Handler", func() {
	t := newTestDriver()

	Specify("should have a non-empty name", func() {
		Expect(t.handler.GetName()).NotTo(BeEmpty())
	})

	Specify("should support any network plugin", func() {
		plugins := t.handler.GetNetworkPlugins()
		Expect(plugins).To(Equal([]string{event.AnyNetworkPlugin}))
	})

	When("transitioning to a non-gateway node", func() {
		BeforeEach(func() {
			vxlanLinkV4 := &netlink.Vxlan{
				LinkAttrs: netlink.LinkAttrs{
					Name:  vxlan.GetVxlanInterfaceName(k8snet.IPv4),
					Index: 10,
				},
			}
			Expect(t.netLink.LinkAdd(vxlanLinkV4)).To(Succeed())

			Expect(t.netLink.RouteAdd(&netlink.Route{
				LinkIndex: vxlanLinkV4.Index,
				Dst: &net.IPNet{
					IP:   net.ParseIP("10.0.0.0"),
					Mask: net.CIDRMask(16, 32),
				},
				Table: vxlan.TableID,
			})).To(Succeed())

			vxlanLinkV6 := &netlink.Vxlan{
				LinkAttrs: netlink.LinkAttrs{
					Name:  vxlan.GetVxlanInterfaceName(k8snet.IPv6),
					Index: 11,
				},
			}
			Expect(t.netLink.LinkAdd(vxlanLinkV6)).To(Succeed())

			Expect(t.netLink.RouteAdd(&netlink.Route{
				LinkIndex: vxlanLinkV6.Index,
				Dst: &net.IPNet{
					IP:   net.ParseIP("fd12:3456:789a:2::"),
					Mask: net.CIDRMask(64, 128),
				},
				Table: vxlan.TableID,
			})).To(Succeed())
		})

		Context("and the local endpoint uses the VXLAN cable driver", func() {
			It("should delete the VXLAN interfaces", func() {
				endpoint := t.createLocalEndpointWithBackend(vxlan.CableDriverName)
				t.DeleteEndpoint(endpoint.Name)

				Eventually(func(g Gomega) {
					_, err := t.netLink.LinkByName(vxlan.GetVxlanInterfaceName(k8snet.IPv4))
					g.Expect(netlinkAPI.IsLinkNotFoundError(err)).To(BeTrue())

					_, err = t.netLink.LinkByName(vxlan.GetVxlanInterfaceName(k8snet.IPv6))
					g.Expect(netlinkAPI.IsLinkNotFoundError(err)).To(BeTrue())
				}).Should(Succeed())
			})
		})

		Context("and the local endpoint uses a different cable driver", func() {
			It("should not delete the VXLAN interfaces", func() {
				endpoint := t.createLocalEndpointWithBackend("libreswan")
				t.DeleteEndpoint(endpoint.Name)

				Consistently(func(g Gomega) {
					_, err := t.netLink.LinkByName(vxlan.GetVxlanInterfaceName(k8snet.IPv4))
					g.Expect(err).NotTo(HaveOccurred())

					_, err = t.netLink.LinkByName(vxlan.GetVxlanInterfaceName(k8snet.IPv6))
					g.Expect(err).NotTo(HaveOccurred())
				}).Should(Succeed())
			})
		})
	})
})

type testDriver struct {
	*eventtesting.ControllerSupport
	handler event.Handler
	netLink *fakeNetlink.NetLink
}

func newTestDriver() *testDriver {
	t := &testDriver{
		ControllerSupport: eventtesting.NewControllerSupport(),
	}

	BeforeEach(func() {
		t.netLink = fakeNetlink.New()
		netlinkAPI.NewFunc = func() netlinkAPI.Interface {
			return t.netLink
		}
	})

	JustBeforeEach(func() {
		t.handler = cabledriver.NewVXLANCleanup()
		t.ControllerSupport.Start(t.handler)
	})

	return t
}

func (t *testDriver) createLocalEndpointWithBackend(backend string) *submv1.Endpoint {
	endpoint := eventtesting.NewEndpoint(eventtesting.LocalClusterID, t.Hostname)
	endpoint.Spec.Backend = backend

	return t.CreateEndpoint(endpoint)
}
