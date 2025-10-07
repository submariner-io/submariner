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
	"context"
	"flag"
	"fmt"
	"net"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cable/vxlan"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/endpoint"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	fakeNetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	"github.com/submariner-io/submariner/pkg/types"
	pkgvxlan "github.com/submariner-io/submariner/pkg/vxlan"
	"github.com/vishvananda/netlink"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	k8snet "k8s.io/utils/net"
)

func init() {
	kzerolog.AddFlags(nil)
}

var _ = BeforeSuite(func() {
	flags := flag.NewFlagSet("kzerolog", flag.ExitOnError)
	kzerolog.AddFlags(flags)
	_ = flags.Parse([]string{"-v=4"})

	kzerolog.InitK8sLogging()
})

func TestVxlan(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Vxlan Cable Driver Suite")
}

const (
	cniIPAddress   = "192.168.5.1"
	cniIPv6Address = "fd12:3456:789a:1::1"
)

var _ = Describe("Vxlan", func() {
	t := newTestDriver()

	Context("IPv4", func() {
		testVxlanConnectivity(t, &natdiscovery.NATEndpointInfo{
			Endpoint: subv1.Endpoint{
				Spec: subv1.EndpointSpec{
					ClusterID:  "east",
					CableName:  "submariner-cable-east-192-68-2-1",
					PrivateIPs: []string{"192.68.2.1"},
					Subnets:    []string{"20.0.0.0/16", "21.0.0.0/16"},
				},
			},
			UseIP:     "172.93.2.1",
			UseNAT:    true,
			UseFamily: k8snet.IPv4,
		})
	})

	Context("IPv6", func() {
		testVxlanConnectivity(t, &natdiscovery.NATEndpointInfo{
			Endpoint: subv1.Endpoint{
				Spec: subv1.EndpointSpec{
					ClusterID:  "east",
					CableName:  "submariner-cable-east-2002-1234-abcd-ffff-c0a8-101",
					PrivateIPs: []string{"2002::1234:abcd:ffff:c0a8:101"},
					Subnets:    []string{"2001::1234:abcd:ffff:c0a8:101/64"},
				},
			},
			UseIP:     "2003:db8:3333:4444:5555:6666:7777:8888",
			UseFamily: k8snet.IPv6,
		})
	})

	Context("dual stack", func() {
		testVxlanConnectivity(t, &natdiscovery.NATEndpointInfo{
			Endpoint: subv1.Endpoint{
				Spec: subv1.EndpointSpec{
					ClusterID:  "east",
					CableName:  "submariner-cable-east-dual-stack",
					PrivateIPs: []string{"192.68.2.1", "2002::1234:abcd:ffff:c0a8:101"},
					Subnets:    []string{"20.0.0.0/16", "2001::1234:abcd:ffff:c0a8:101/64"},
				},
			},
			UseIP:     "172.93.2.1", // Test with IPv4 on dual-stack endpoint
			UseFamily: k8snet.IPv4,
		})
	})
})

func testVxlanConnectivity(t *testDriver, natInfo *natdiscovery.NATEndpointInfo) {
	var expectedVTepPrefix string

	BeforeEach(func() {
		if natInfo.UseFamily == k8snet.IPv6 {
			expectedVTepPrefix = vxlan.VxlanVTepNetworkPrefixCIDRv6
		} else {
			expectedVTepPrefix = vxlan.VxlanVTepNetworkPrefixCIDR
		}
	})

	JustBeforeEach(func() {
		// Wait for the family-specific interface
		interfaceName := vxlan.GetVxlanInterfaceName(natInfo.UseFamily)
		link := t.netLink.AwaitLink(interfaceName)
		vxLan, ok := link.(*netlink.Vxlan)
		Expect(ok).To(BeTrue(), "Unexpected Link type: %T", link)

		Expect(vxLan.Port).To(Equal(vxlan.DefaultPort))

		t.netLink.AwaitRule(vxlan.TableID, "", "")
	})

	Specify("ConnectToEndpoint should create a Connection and add expected data-plane components", func() {
		ip, err := t.driver.ConnectToEndpoint(natInfo)
		Expect(err).To(Succeed())
		Expect(ip).To(Equal(natInfo.UseIP))

		t.assertConnection(natInfo)

		// FDB entries should use endpoint IP, not VTEP IP (matches working devel branch)
		t.netLink.AwaitNeighbors(0, natInfo.UseIP)

		// Use family-specific interface name
		interfaceName := vxlan.GetVxlanInterfaceName(natInfo.UseFamily)
		link, err := t.netLink.LinkByName(interfaceName)
		Expect(err).To(Succeed())

		routes, err := t.netLink.RouteList(link, k8snet.IPFamilyUnknown)
		Expect(err).To(Succeed())

		var actualRoutes []map[string]string
		for i := range routes {
			actualRoutes = append(actualRoutes, routeFieldMap(routes[i].Src.String(), routes[i].Gw.String(), routes[i].Dst.String()))
		}

		_, cidrNet, err := net.ParseCIDR(expectedVTepPrefix)
		Expect(err).To(Succeed())

		var gw string

		if natInfo.UseFamily == k8snet.IPv6 {
			// For IPv6, derive VTEP IP from the private IP
			privateIP := natInfo.Endpoint.Spec.GetPrivateIP(natInfo.UseFamily)
			vtepIP, err := pkgvxlan.GetVtepIPAddressFrom(privateIP, expectedVTepPrefix, natInfo.UseFamily)
			Expect(err).To(Succeed())

			gw = vtepIP.String()
		} else {
			// For IPv4, use the original logic
			prefix := cidrNet.IP.To4()
			Expect(prefix).ToNot(BeNil(), "invalid IPv4 prefix in "+expectedVTepPrefix)
			gw = fmt.Sprintf("%d.68.2.1", prefix[0])
		}

		// Filter subnets by the family we're testing
		allowedIPs := natInfo.Endpoint.Spec.ParseSubnets(natInfo.UseFamily)

		// Use the appropriate CNI IP based on the family
		cniIP := cniIPAddress
		if natInfo.UseFamily == k8snet.IPv6 {
			cniIP = cniIPv6Address
		}

		var expectedRoutes []map[string]string
		for _, subnet := range allowedIPs {
			expectedRoutes = append(expectedRoutes, routeFieldMap(cniIP, gw, subnet.String()))
		}

		Expect(actualRoutes).To(HaveExactElements(expectedRoutes))
	})

	Specify("DisconnectFromEndpoint should remove the Connection and its data-plane components", func() {
		_, err := t.driver.ConnectToEndpoint(natInfo)
		Expect(err).To(Succeed())

		Expect(t.driver.DisconnectFromEndpoint(&types.SubmarinerEndpoint{Spec: natInfo.Endpoint.Spec}, natInfo.UseFamily)).To(Succeed())
		t.assertNoConnection(natInfo)
		t.netLink.AwaitNoNeighbors(0, natInfo.UseIP)

		// Use family-specific interface name
		interfaceName := vxlan.GetVxlanInterfaceName(natInfo.UseFamily)
		link, err := t.netLink.LinkByName(interfaceName)
		Expect(err).To(Succeed())

		routes, err := t.netLink.RouteList(link, k8snet.IPFamilyUnknown)
		Expect(err).To(Succeed())
		Expect(routes).To(BeEmpty())
	})

	Specify("Cleanup should remove the VxLAN link device", func() {
		Expect(t.driver.Cleanup()).To(Succeed())
		t.netLink.AwaitNoLink(vxlan.VxlanIface)
		t.netLink.AwaitNoRule(vxlan.TableID, "", "")
	})
}

func routeFieldMap(src, gw, dst string) map[string]string {
	return map[string]string{
		"Src": src,
		"Gw":  dst,
		"Dst": gw,
	}
}

type testDriver struct {
	localEndpoint subv1.EndpointSpec
	localCluster  *types.SubmarinerCluster
	netLink       *fakeNetlink.NetLink
	driver        cable.Driver
}

func newTestDriver() *testDriver {
	t := &testDriver{}

	BeforeEach(func() {
		t.localCluster = &types.SubmarinerCluster{
			Spec: subv1.ClusterSpec{
				ClusterID:   "local",
				ServiceCIDR: []string{"10.0.0.0/16", "fd12:3456:789a:1::/112"},
				ClusterCIDR: []string{cniIPAddress + "/24", cniIPv6Address + "/64"},
			},
		}

		t.localEndpoint = subv1.EndpointSpec{
			ClusterID:  t.localCluster.Spec.ClusterID,
			CableName:  "submariner-cable-local-192-68-1-1",
			PrivateIPs: []string{"192.68.1.1", "fd12:3456:789a:1::1"}, // Add IPv6 for dual-stack testing
			Subnets:    append(t.localCluster.Spec.ServiceCIDR, t.localCluster.Spec.ClusterCIDR...),
		}

		t.netLink = fakeNetlink.New()
		netlinkAPI.NewFunc = func() netlinkAPI.Interface {
			return t.netLink
		}

		t.netLink.SetupDefaultGateway(k8snet.IPv4, net.Interface{Index: 99, MTU: 10})
		t.netLink.SetupDefaultGateway(k8snet.IPv6, net.Interface{Index: 100, MTU: 10})

		cni.HostInterfaces = func() ([]cni.HostInterface, error) {
			return []cni.HostInterface{
				{
					Name: "veth0",
					Addr: cniIPAddress + "/24",
				},
				{
					Name: "veth0",
					Addr: cniIPv6Address + "/64",
				},
			}, nil
		}
	})

	JustBeforeEach(func() {
		d, err := vxlan.NewDriver(
			endpoint.NewLocal(&t.localEndpoint, dynamicfake.NewSimpleDynamicClient(scheme.Scheme), ""),
			t.localCluster, nil)
		Expect(err).To(Succeed())

		Expect(d.Init(context.TODO())).To(Succeed())
		Expect(d.GetName()).To(Equal(vxlan.CableDriverName))

		t.driver = d
	})

	return t
}

func (t *testDriver) assertConnection(natInfo *natdiscovery.NATEndpointInfo) {
	conn := subv1.Connection{
		Status:   subv1.Connected,
		Endpoint: natInfo.Endpoint.Spec,
		UsingIP:  natInfo.UseIP,
		UsingNAT: natInfo.UseNAT,
	}

	conns, err := t.driver.GetActiveConnections()
	Expect(err).To(Succeed())
	Expect(conns).To(HaveExactElements(conn))

	conns, err = t.driver.GetConnections()
	Expect(err).To(Succeed())
	Expect(conns).To(HaveExactElements(conn))
}

func (t *testDriver) assertNoConnection(natInfo *natdiscovery.NATEndpointInfo) {
	conn, err := t.driver.GetActiveConnections()
	Expect(err).To(Succeed())
	Expect(conn).ToNot(HaveExactElements(subv1.Connection{
		Status:   subv1.Connected,
		Endpoint: natInfo.Endpoint.Spec,
		UsingIP:  natInfo.UseIP,
		UsingNAT: natInfo.UseNAT,
	}))
}
