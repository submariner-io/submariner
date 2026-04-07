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

package endpoint_test

import (
	"errors"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/endpoint"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	fakeNetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	"github.com/vishvananda/netlink"
	k8snet "k8s.io/utils/net"
)

var _ = Describe("GetLocalIP", func() {
	const (
		ipv4DialIP           = "100.10.1.0"
		ipv4RouteIP          = "200.10.1.0"
		ipv6RouteIP          = "2002:0:0:1234::"
		ipv6LinkLocalUnicast = "fe80::10"
	)

	var (
		ipv6DialIP string
		netLink    *fakeNetlink.NetLink
	)

	BeforeEach(func() {
		ipv6DialIP = ""

		netLink = fakeNetlink.New()
		netlinkAPI.NewFunc = func() netlinkAPI.Interface {
			return netLink
		}

		defaultDial := endpoint.Dial
		DeferCleanup(func() {
			endpoint.Dial = defaultDial
		})

		endpoint.Dial = func(network, _ string) (net.Conn, error) {
			if network == "udp4" {
				return &fakeConn{ip: net.ParseIP(ipv4DialIP)}, nil
			} else if network == "udp6" {
				return &fakeConn{ip: net.ParseIP(ipv6DialIP)}, nil
			}

			return nil, errors.New("invalid network: " + network)
		}
	})

	Context("IPv4", func() {
		When("Dial succeeds", func() {
			It("should return the IP", func() {
				Expect(endpoint.GetLocalIP(k8snet.IPv4)).To(Equal(ipv4DialIP))
			})
		})

		When("Dial fails", func() {
			BeforeEach(func() {
				endpoint.Dial = func(_, _ string) (net.Conn, error) {
					return nil, errors.New("mock error")
				}

				Expect(netLink.RouteAdd(&netlink.Route{
					LinkIndex: 1,
					Src:       net.ParseIP(ipv4RouteIP),
					Gw:        net.ParseIP("1.2.0.0"),
				})).To(Succeed())
			})

			It("should return a route local IP", func() {
				Expect(endpoint.GetLocalIP(k8snet.IPv4)).To(Equal(ipv4RouteIP))
			})
		})
	})

	Context("IPv6", func() {
		BeforeEach(func() {
			ipv6DialIP = "2001:0:0:4321::"

			cni.HostInterfaces = func() ([]cni.HostInterface, error) {
				return []cni.HostInterface{}, nil
			}
		})

		When("Dial succeeds", func() {
			Context("and the IP is usable", func() {
				It("should return the IP", func() {
					Expect(endpoint.GetLocalIP(k8snet.IPv6)).To(Equal(ipv6DialIP))
				})
			})

			Context("and the IP isn't usable and no other IP on the same interface exists", func() {
				It("should return a route local IP", func() {
					Expect(netLink.RouteAdd(&netlink.Route{
						LinkIndex: 2,
						Src:       net.ParseIP("fd69::2"),
						Gw:        net.ParseIP("2001:0:0:0::"),
					})).To(Succeed())

					Expect(netLink.RouteAdd(&netlink.Route{
						LinkIndex: 2,
						Src:       net.ParseIP(ipv6RouteIP),
						Gw:        net.ParseIP("2001:0:0:0::"),
					})).To(Succeed())

					ipv6DialIP = net.IPv6loopback.String()
					Expect(endpoint.GetLocalIP(k8snet.IPv6)).To(Equal(ipv6RouteIP))

					ipv6DialIP = ipv6LinkLocalUnicast // link-local unicast address
					Expect(endpoint.GetLocalIP(k8snet.IPv6)).To(Equal(ipv6RouteIP))

					cni.HostInterfaces = func() ([]cni.HostInterface, error) {
						return []cni.HostInterface{
							{
								Name: "v6-owns",
								Addr: net.ParseIP("fe80::14b:b63:c7e1:1558"),
							},
						}, nil
					}

					ipv6DialIP = ipv6LinkLocalUnicast
					Expect(endpoint.GetLocalIP(k8snet.IPv6)).To(Equal(ipv6RouteIP))
				})
			})

			Context("and the IP isn't usable and another IP on the same interface exists", func() {
				It("should return another usable IPv6 from the same interface", func() {
					cni.HostInterfaces = func() ([]cni.HostInterface, error) {
						return []cni.HostInterface{
							{
								Name: "v4",
								Addr: net.ParseIP("10.34.1.0"),
							},
							{
								Name: "v6-not-owns",
								Addr: net.ParseIP("ff00::1"),
							},
							{
								Name: "v6-other",
								Addr: net.ParseIP("2001::14b:b63:c7e1:123"),
							},
							{
								Name: "v6-owns",
								Addr: net.ParseIP("fe80::14b:b63:c7e1:1558"),
							},
							{
								Name: "v6-owns",
								Addr: net.ParseIP("fc00::14b:b63:c7e1:aaa"),
							},
						}, nil
					}

					ipv6DialIP = "fe80::14b:b63:c7e1:1558"
					Expect(endpoint.GetLocalIP(k8snet.IPv6)).To(Equal("fc00::14b:b63:c7e1:aaa"))
				})
			})
		})
	})
})

type fakeConn struct {
	net.TCPConn
	ip net.IP
}

func (f *fakeConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: f.ip}
}

func (f *fakeConn) Close() error {
	return nil
}
