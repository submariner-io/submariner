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
	"flag"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cable/vxlan"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	fakeNetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/vishvananda/netlink"
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

const cniIPAddress = "192.168.5.1"

var _ = Describe("Vxlan", func() {
	t := newTestDriver()

	var natInfo *natdiscovery.NATEndpointInfo

	BeforeEach(func() {
		natInfo = &natdiscovery.NATEndpointInfo{
			Endpoint: subv1.Endpoint{
				Spec: subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-192-68-2-1",
					PrivateIP: "192.68.2.1",
					Subnets:   []string{"20.0.0.0/16", "21.0.0.0/16"},
				},
			},
			UseIP:  "172.93.2.1",
			UseNAT: true,
		}
	})

	JustBeforeEach(func() {
		link := t.netLink.AwaitLink(vxlan.VxlanIface)
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
		t.netLink.AwaitNeighbors(0, natInfo.UseIP)

		link, err := t.netLink.LinkByName(vxlan.VxlanIface)
		Expect(err).To(Succeed())

		routes, err := t.netLink.RouteList(link, 0)
		Expect(err).To(Succeed())

		var actualRoutes []map[string]string
		for i := range routes {
			actualRoutes = append(actualRoutes, routeFieldMap(routes[i].Src.String(), routes[i].Gw.String(), routes[i].Dst.String()))
		}

		gw := fmt.Sprintf("%d.68.2.1", vxlan.VxlanVTepNetworkPrefix)
		Expect(actualRoutes).To(HaveExactElements(routeFieldMap(cniIPAddress, gw, natInfo.Endpoint.Spec.Subnets[0]),
			routeFieldMap(cniIPAddress, gw, natInfo.Endpoint.Spec.Subnets[1])))
	})

	Specify("DisconnectFromEndpoint should remove the Connection and its data-plane components", func() {
		_, err := t.driver.ConnectToEndpoint(natInfo)
		Expect(err).To(Succeed())

		Expect(t.driver.DisconnectFromEndpoint(&types.SubmarinerEndpoint{Spec: natInfo.Endpoint.Spec})).To(Succeed())
		t.assertNoConnection(natInfo)
		t.netLink.AwaitNoNeighbors(0, natInfo.UseIP)

		link, err := t.netLink.LinkByName(vxlan.VxlanIface)
		Expect(err).To(Succeed())

		routes, err := t.netLink.RouteList(link, 0)
		Expect(err).To(Succeed())
		Expect(routes).To(BeEmpty())
	})

	Specify("Cleanup should remove the VxLAN link device", func() {
		Expect(t.driver.Cleanup()).To(Succeed())
		t.netLink.AwaitNoLink(vxlan.VxlanIface)
		t.netLink.AwaitNoRule(vxlan.TableID, "", "")
	})
})

func routeFieldMap(src, gw, dst string) map[string]string {
	return map[string]string{
		"Src": src,
		"Gw":  dst,
		"Dst": gw,
	}
}

type testDriver struct {
	localEndpoint *types.SubmarinerEndpoint
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
				ServiceCIDR: []string{"10.0.0.0/16"},
				ClusterCIDR: []string{"11.0.0.0/16"},
			},
		}

		t.localEndpoint = &types.SubmarinerEndpoint{Spec: subv1.EndpointSpec{
			ClusterID: t.localCluster.Spec.ClusterID,
			CableName: "submariner-cable-local-192-68-1-1",
			PrivateIP: "192.68.1.1",
			Subnets:   append(t.localCluster.Spec.ServiceCIDR, t.localCluster.Spec.ClusterCIDR...),
		}}

		t.netLink = fakeNetlink.New()
		netlinkAPI.NewFunc = func() netlinkAPI.Interface {
			return t.netLink
		}

		cni.DiscoverFunc = func(_ []string) (*cni.Interface, error) {
			return &cni.Interface{
				Name:      "veth0",
				IPAddress: cniIPAddress,
			}, nil
		}
	})

	JustBeforeEach(func() {
		d, err := vxlan.NewDriver(t.localEndpoint, t.localCluster)
		Expect(err).To(Succeed())

		Expect(d.Init()).To(Succeed())
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
