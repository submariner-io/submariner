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
	"net"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cidr"
	"github.com/submariner-io/submariner/pkg/cni"
	evtesting "github.com/submariner-io/submariner/pkg/event/testing"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	fakeNetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	fakePF "github.com/submariner-io/submariner/pkg/packetfilter/fake"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/kubeproxy"
	"github.com/submariner-io/submariner/pkg/vxlan"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	k8snet "k8s.io/utils/net"
)

const (
	cniIP4Address           = "192.168.5.1"
	cniIP6Address           = "2001:0:0:1234::"
	localClusterIPv4CIDR    = cniIP4Address + "/24"
	localClusterIPv6CIDR    = cniIP6Address + "/64"
	localServiceIPv4CIDR    = "169.254.2.0/24"
	localServiceIPv6CIDR    = "2003:0:0:1234::"
	remoteIPv4Subnet1       = "170.250.1.0/24"
	remoteIPv4Subnet2       = "171.250.1.0/24"
	remoteIPv6Subnet        = "2002:0:0:1234::/64"
	localNodeName1          = "local-node1"
	localNodeName2          = "local-node2"
	remoteNodeName          = "remote-node"
	nodeIPv4Address1        = "10.253.10.2"
	nodeIPv4Address2        = "10.253.10.3"
	nodeIPv6Address         = "2004:0:0:1234::"
	hostInterfaceIndex      = 100
	vxLanInterfaceIndex     = 200
	vxLanInterfaceIndexIPv6 = 300
	hostInterfaceMTU        = 101
)

func init() {
	kzerolog.AddFlags(nil)
}

var _ = BeforeSuite(func() {
	kzerolog.InitK8sLogging()
})

func TestKubeProxyIPTables(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Kubeproxy IP Tables Suite")
}

type testDriver struct {
	*evtesting.ControllerSupport
	handler           *kubeproxy.SyncHandler
	ipFamily          k8snet.IPFamily
	pFilter           *fakePF.PacketFilter
	netLink           *fakeNetlink.NetLink
	localEndpoint     *submarinerv1.Endpoint
	remoteEndpoint    *submarinerv1.Endpoint
	hostInterfaceAddr string
	localClusterCIDRs []string
	localServiceCIDRs []string
}

func newTestDriver() *testDriver {
	t := &testDriver{
		ControllerSupport: evtesting.NewControllerSupport(),
	}

	BeforeEach(func() {
		t.ipFamily = k8snet.IPv4
		t.hostInterfaceAddr = "172.19.3.1/24"

		t.netLink = fakeNetlink.New()
		t.netLink.SetLinkIndex(kubeproxy.GetVxLANInterfaceName(k8snet.IPv4), vxLanInterfaceIndex)
		t.netLink.SetLinkIndex(kubeproxy.GetVxLANInterfaceName(k8snet.IPv6), vxLanInterfaceIndexIPv6)

		netlinkAPI.NewFunc = func() netlinkAPI.Interface {
			return t.netLink
		}
		t.pFilter = fakePF.New(k8snet.IPv4, k8snet.IPv6)

		cni.HostInterfaces = func() ([]cni.HostInterface, error) {
			return []cni.HostInterface{
				{
					Name: "veth0",
					Addr: localClusterIPv4CIDR,
				},
				{
					Name: "veth1",
					Addr: localClusterIPv6CIDR,
				},
			}, nil
		}

		t.localEndpoint = newLocalEndpoint(localNodeName1)
		t.remoteEndpoint = newRemoteEndpoint()

		t.localClusterCIDRs = []string{localClusterIPv4CIDR}
		t.localServiceCIDRs = []string{localServiceIPv4CIDR}
	})

	JustBeforeEach(func() {
		_, cidr, err := net.ParseCIDR(t.hostInterfaceAddr)
		Expect(err).NotTo(HaveOccurred())

		t.netLink.SetupDefaultGateway(t.ipFamily, net.Interface{
			Index: hostInterfaceIndex,
			MTU:   hostInterfaceMTU,
			Name:  "gw-intf",
		}, cidr)

		t.netLink.SetAllowedIPFamilies(t.ipFamily)

		t.handler = kubeproxy.NewSyncHandler(t.ipFamily, t.localClusterCIDRs, t.localServiceCIDRs)
		t.Start(t.handler)
	})

	return t
}

func (t *testDriver) getVxLanInterfaceIndex() int {
	if t.ipFamily == k8snet.IPv4 {
		return vxLanInterfaceIndex
	}

	return vxLanInterfaceIndexIPv6
}

func (t *testDriver) verifyVxLANRoutes() {
	t.netLink.AwaitDstRoutes(t.awaitVxlanLink().Attrs().Index, 0,
		cidr.ExtractSubnets(t.ipFamily, t.remoteEndpoint.Spec.Subnets)...)
}

func (t *testDriver) verifyNoVxLANRoutes() {
	time.Sleep(200 * time.Millisecond)
	t.netLink.AwaitNoDstRoutes(vxLanInterfaceIndex, 0, t.remoteEndpoint.Spec.Subnets...)
}

func (t *testDriver) verifyHostNetworkingRoutes() {
	t.netLink.AwaitDstRoutes(hostInterfaceIndex, constants.RouteAgentHostNetworkTableID,
		cidr.ExtractSubnets(t.ipFamily, t.remoteEndpoint.Spec.Subnets)...)
}

func (t *testDriver) verifyNoHostNetworkingRoutes() {
	time.Sleep(200 * time.Millisecond)
	t.netLink.AwaitNoDstRoutes(hostInterfaceIndex, constants.RouteAgentHostNetworkTableID, t.remoteEndpoint.Spec.Subnets...)
}

func (t *testDriver) verifyRemoteSubnetIPTableRules() {
	for _, remoteCIDR := range cidr.ExtractSubnets(t.ipFamily, t.remoteEndpoint.Spec.Subnets) {
		t.pFilter.AwaitRule(packetfilter.TableTypeNAT, constants.SmPostRoutingChain,
			And(ContainSubstring(cidr.ExtractSubnets(t.ipFamily, t.localClusterCIDRs)[0]), ContainSubstring(remoteCIDR)))
	}
}

func (t *testDriver) addVxLANRoute(cidr string) {
	_, err := vxlan.NewInterface(&vxlan.Attributes{
		Name: kubeproxy.GetVxLANInterfaceName(k8snet.IPv4),
	}, t.netLink)
	Expect(err).To(Succeed())

	_, dst, err := net.ParseCIDR(cidr)
	Expect(err).To(Succeed())

	err = t.netLink.RouteAdd(&netlink.Route{
		Dst:       dst,
		Gw:        net.IPv4(11, 21, 31, 41),
		Scope:     unix.RT_SCOPE_UNIVERSE,
		LinkIndex: vxLanInterfaceIndex,
		Protocol:  4,
	})
	Expect(err).To(Succeed())
}

func (t *testDriver) awaitVxlanLink() *netlink.Vxlan {
	return toVxlan(t.netLink.AwaitLink(kubeproxy.GetVxLANInterfaceName(t.ipFamily)))
}

func newLocalEndpoint(hostname string) *submarinerv1.Endpoint {
	return &submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: string(uuid.NewUUID()),
		},
		Spec: submarinerv1.EndpointSpec{
			CableName:  "submariner-cable-local-192-68-1-2",
			ClusterID:  evtesting.LocalClusterID,
			PrivateIPs: []string{"192.68.1.2"},
			Hostname:   hostname,
			Backend:    "libreswan",
		},
	}
}

func newRemoteEndpoint() *submarinerv1.Endpoint {
	return &submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: string(uuid.NewUUID()),
		},
		Spec: submarinerv1.EndpointSpec{
			CableName:  "submariner-cable-remote-192-68-1-2",
			ClusterID:  "remote",
			PrivateIPs: []string{"192.68.1.2"},
			Hostname:   remoteNodeName,
			Subnets:    []string{remoteIPv4Subnet1, remoteIPv4Subnet2},
			Backend:    "libreswan",
		},
	}
}

func newNode(addr string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: string(uuid.NewUUID()),
		},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{
					Type: corev1.NodeExternalDNS,
				},
				{
					Type:    corev1.NodeInternalIP,
					Address: addr,
				},
			},
		},
	}
}

func toVxlan(link netlink.Link) *netlink.Vxlan {
	vxLan, ok := link.(*netlink.Vxlan)
	Expect(ok).To(BeTrue(), "Unexpected Link type: %T", link)

	return vxLan
}
