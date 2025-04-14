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
	"github.com/submariner-io/submariner/pkg/cni"
	evtesting "github.com/submariner-io/submariner/pkg/event/testing"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	fakeNetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	fakePF "github.com/submariner-io/submariner/pkg/packetfilter/fake"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/kubeproxy"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	k8snet "k8s.io/utils/net"
)

const (
	cniIPAddress     = "192.168.5.1"
	localClusterCIDR = cniIPAddress + "/24"
	localServiceCIDR = "169.254.2.0/24"
	remoteSubnet1    = "170.250.1.0/24"
	remoteSubnet2    = "171.250.1.0/24"
	localNodeName1   = "local-node1"
	localNodeName2   = "local-node2"
	remoteNodeName   = "remote-node"
	nodeAddress1     = "10.253.10.2"
	nodeAddress2     = "10.253.10.3"
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
	handler             *kubeproxy.SyncHandler
	pFilter             *fakePF.PacketFilter
	netLink             *fakeNetlink.NetLink
	localEndpoint       *submarinerv1.Endpoint
	remoteEndpoint      *submarinerv1.Endpoint
	hostInterfaceIndex  int
	vxLanInterfaceIndex int
}

func newTestDriver() *testDriver {
	t := &testDriver{
		ControllerSupport: evtesting.NewControllerSupport(),
	}

	BeforeEach(func() {
		defaultHostIface, err := netlinkAPI.GetDefaultGatewayInterface(k8snet.IPv4)
		Expect(err).To(Succeed())

		t.hostInterfaceIndex = defaultHostIface.Index
		t.vxLanInterfaceIndex = t.hostInterfaceIndex + 1

		t.netLink = fakeNetlink.New()
		t.netLink.SetLinkIndex(kubeproxy.VxLANIface, t.vxLanInterfaceIndex)

		netlinkAPI.NewFunc = func() netlinkAPI.Interface {
			return t.netLink
		}
		t.pFilter = fakePF.New()

		cni.HostInterfaces = func() ([]cni.HostInterface, error) {
			return []cni.HostInterface{{
				Name: "veth0",
				Addr: cniIPAddress + "/24",
			}}, nil
		}

		t.localEndpoint = newLocalEndpoint(localNodeName1)
		t.remoteEndpoint = newRemoteEndpoint()

		t.handler = kubeproxy.NewSyncHandler([]string{localClusterCIDR}, []string{localServiceCIDR})

		t.Start(t.handler)
	})

	return t
}

func (t *testDriver) verifyVxLANRoutes() {
	t.netLink.AwaitDstRoutes(t.netLink.AwaitLink(kubeproxy.VxLANIface).Attrs().Index, 0, t.remoteEndpoint.Spec.Subnets...)
}

func (t *testDriver) verifyNoVxLANRoutes() {
	time.Sleep(200 * time.Millisecond)
	t.netLink.AwaitNoDstRoutes(t.vxLanInterfaceIndex, 0, t.remoteEndpoint.Spec.Subnets...)
}

func (t *testDriver) verifyHostNetworkingRoutes() {
	t.netLink.AwaitDstRoutes(t.hostInterfaceIndex, constants.RouteAgentHostNetworkTableID, t.remoteEndpoint.Spec.Subnets...)
}

func (t *testDriver) verifyNoHostNetworkingRoutes() {
	time.Sleep(200 * time.Millisecond)
	t.netLink.AwaitNoDstRoutes(t.hostInterfaceIndex, constants.RouteAgentHostNetworkTableID, t.remoteEndpoint.Spec.Subnets...)
}

func (t *testDriver) verifyRemoteSubnetIPTableRules() {
	for _, remoteCIDR := range t.remoteEndpoint.Spec.Subnets {
		t.pFilter.AwaitRule(packetfilter.TableTypeNAT, constants.SmPostRoutingChain,
			And(ContainSubstring(localClusterCIDR), ContainSubstring(remoteCIDR)))
	}
}

func (t *testDriver) addVxLANRoute(cidr string) {
	_, dst, err := net.ParseCIDR(cidr)
	Expect(err).To(Succeed())

	_ = t.netLink.RouteAdd(&netlink.Route{
		Dst:       dst,
		Gw:        net.IPv4(11, 21, 31, 41),
		Scope:     unix.RT_SCOPE_UNIVERSE,
		LinkIndex: t.vxLanInterfaceIndex,
		Protocol:  4,
	})
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
			Subnets:    []string{remoteSubnet1, remoteSubnet2},
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
