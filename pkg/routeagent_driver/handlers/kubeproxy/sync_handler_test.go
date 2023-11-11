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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event/testing"
	"github.com/submariner-io/submariner/pkg/iptables"
	fakeIPT "github.com/submariner-io/submariner/pkg/iptables/fake"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	fakeNetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/cni"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/kubeproxy"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
)

const (
	localClusterCIDR = "169.254.1.0/24"
	localServiceCIDR = "169.254.2.0/24"
	remoteSubnet1    = "170.250.1.0/24"
	remoteSubnet2    = "171.250.1.0/24"
	localNodeName1   = "local-node1"
	localNodeName2   = "local-node2"
	remoteNodeName   = "remote-node"
	nodeAddress1     = "10.253.10.2"
	nodeAddress2     = "10.253.10.3"
	cniIPAddress     = "192.168.5.1"
)

var _ = Describe("SyncHandler", func() {
	Describe("Endpoints", testEndpoints)
	Describe("Gateway transition", testGatewayTransition)
	Describe("Nodes", testNodes)
	Describe("Uninstall", testUninstall)
})

func testEndpoints() {
	t := newTestDriver()

	When("a local Endpoint is created while on a non-gateway node", func() {
		JustBeforeEach(func() {
			t.CreateEndpoint(t.localEndpoint)
		})

		It("should add the VxLAN interface", func() {
			Expect(toVxlan(t.netLink.AwaitLink(kubeproxy.VxLANIface)).Group.String()).To(Equal(t.localEndpoint.Spec.PrivateIP))
		})

		Context("and old VxLAN routes are present", func() {
			BeforeEach(func() {
				t.addVxLANRoute(remoteSubnet1)
			})

			It("should remove them", func() {
				t.netLink.AwaitNoRoutes(t.vxLanInterfaceIndex, remoteSubnet1)
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

func testGatewayTransition() {
	t := newTestDriver()

	When("transition to gateway", func() {
		var localHostEP *submarinerv1.Endpoint

		JustBeforeEach(func() {
			t.CreateEndpoint(t.remoteEndpoint)
			localHostEP = t.CreateLocalHostEndpoint()
		})

		It("should add the VxLAN interface", func() {
			Expect(toVxlan(t.netLink.AwaitLink(kubeproxy.VxLANIface)).Group).To(BeNil())
		})

		It("should add a routing rule for the RouteAgentHostNetworkTableID", func() {
			t.netLink.AwaitRule(constants.RouteAgentHostNetworkTableID)
		})

		It("should add host networking routing rules for the remote subnets", func() {
			t.verifyHostNetworkingRoutes()
		})

		Context("and previous VxLAN routes are present", func() {
			BeforeEach(func() {
				t.addVxLANRoute(remoteSubnet1)
				t.CreateEndpoint(t.localEndpoint)
			})

			It("should remove them", func() {
				t.netLink.AwaitNoRoutes(t.vxLanInterfaceIndex, remoteSubnet1)
			})
		})

		Context("and Node addresses are present", func() {
			BeforeEach(func() {
				t.CreateNode(newNode(nodeAddress1))
				t.CreateNode(newNode(nodeAddress2))
			})

			It("should add an FDB entry on the VxLAN interface for each address", func() {
				t.netLink.AwaitNeighbors(t.vxLanInterfaceIndex, nodeAddress1, nodeAddress2)
			})
		})

		Context("and then to non-gateway", func() {
			JustBeforeEach(func() {
				t.netLink.AwaitRule(constants.RouteAgentHostNetworkTableID)
				t.DeleteEndpoint(localHostEP.Name)
			})

			It("should remove the routing rule for the RouteAgentHostNetworkTableID", func() {
				t.netLink.AwaitNoRule(constants.RouteAgentHostNetworkTableID)
			})

			It("should remove host networking routing rules for the remote subnets", func() {
				t.verifyNoHostNetworkingRoutes()
			})
		})
	})
}

func testNodes() {
	t := newTestDriver()

	var node *corev1.Node

	BeforeEach(func() {
		node = newNode(nodeAddress1)
	})

	When("a Node is created and then deleted on a gateway node", func() {
		JustBeforeEach(func() {
			t.CreateLocalHostEndpoint()
			t.CreateNode(node)
		})

		It("should add/remove an FDB entry on the VxLAN interface for each Node address", func() {
			t.netLink.AwaitNeighbors(t.vxLanInterfaceIndex, nodeAddress1)

			t.DeleteNode(node.Name)
			t.netLink.AwaitNoNeighbors(t.vxLanInterfaceIndex, nodeAddress1)
		})
	})

	When("a Node is created on a non-gateway node", func() {
		JustBeforeEach(func() {
			t.CreateNode(node)
		})

		It("should not add an FDB entry on the VxLAN interface for each Node address", func() {
			t.netLink.AwaitNoNeighbors(t.vxLanInterfaceIndex, nodeAddress1)
		})
	})
}

func testUninstall() {
	t := newTestDriver()

	Context("on Uninstall", func() {
		It("should clean up dataplane artifacts", func() {
			t.CreateLocalHostEndpoint()
			t.netLink.AwaitRule(constants.RouteAgentHostNetworkTableID)

			Expect(t.handler.Uninstall()).To(Succeed())

			t.netLink.AwaitNoRule(constants.RouteAgentHostNetworkTableID)
			t.netLink.AwaitNoLink(kubeproxy.VxLANIface)
			t.verifyNoHostNetworkingRoutes()
		})
	})
}

type testDriver struct {
	*testing.ControllerSupport
	handler             *kubeproxy.SyncHandler
	ipTables            *fakeIPT.IPTables
	netLink             *fakeNetlink.NetLink
	localEndpoint       *submarinerv1.Endpoint
	remoteEndpoint      *submarinerv1.Endpoint
	hostInterfaceIndex  int
	vxLanInterfaceIndex int
}

func newTestDriver() *testDriver {
	t := &testDriver{
		ControllerSupport: testing.NewControllerSupport(),
	}

	BeforeEach(func() {
		defaultHostIface, err := netlinkAPI.GetDefaultGatewayInterface()
		Expect(err).To(Succeed())

		t.hostInterfaceIndex = defaultHostIface.Index
		t.vxLanInterfaceIndex = t.hostInterfaceIndex + 1

		t.netLink = fakeNetlink.New()
		t.netLink.SetLinkIndex(kubeproxy.VxLANIface, t.vxLanInterfaceIndex)

		netlinkAPI.NewFunc = func() netlinkAPI.Interface {
			return t.netLink
		}

		t.ipTables = fakeIPT.New()
		iptables.NewFunc = func() (iptables.Interface, error) {
			return t.ipTables, nil
		}

		cni.DiscoverFunc = func(clusterCIDR string) (*cni.Interface, error) {
			return &cni.Interface{
				Name:      "veth0",
				IPAddress: cniIPAddress,
			}, nil
		}

		t.localEndpoint = newLocalEndpoint(localNodeName1)
		t.remoteEndpoint = newRemoteEndpoint()

		t.handler = kubeproxy.NewSyncHandler([]string{localClusterCIDR}, []string{localServiceCIDR})

		t.Start(t.handler)
	})

	AfterEach(func() {
		iptables.NewFunc = nil
		netlinkAPI.NewFunc = nil
		cni.DiscoverFunc = nil
	})

	return t
}

func (t *testDriver) verifyVxLANRoutes() {
	t.netLink.AwaitRoutes(t.netLink.AwaitLink(kubeproxy.VxLANIface).Attrs().Index, t.remoteEndpoint.Spec.Subnets...)
}

func (t *testDriver) verifyNoVxLANRoutes() {
	time.Sleep(200 * time.Millisecond)
	t.netLink.AwaitNoRoutes(t.vxLanInterfaceIndex, t.remoteEndpoint.Spec.Subnets...)
}

func (t *testDriver) verifyHostNetworkingRoutes() {
	t.netLink.AwaitRoutes(t.hostInterfaceIndex, t.remoteEndpoint.Spec.Subnets...)
}

func (t *testDriver) verifyNoHostNetworkingRoutes() {
	time.Sleep(200 * time.Millisecond)
	t.netLink.AwaitNoRoutes(t.hostInterfaceIndex, t.remoteEndpoint.Spec.Subnets...)
}

func (t *testDriver) verifyRemoteSubnetIPTableRules() {
	for _, remoteCIDR := range t.remoteEndpoint.Spec.Subnets {
		t.ipTables.AwaitRule("nat", constants.SmPostRoutingChain,
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
			CableName: "submariner-cable-local-192-68-1-2",
			ClusterID: testing.LocalClusterID,
			PrivateIP: "192.68.1.2",
			Hostname:  hostname,
			Backend:   "libreswan",
		},
	}
}

func newRemoteEndpoint() *submarinerv1.Endpoint {
	return &submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: string(uuid.NewUUID()),
		},
		Spec: submarinerv1.EndpointSpec{
			CableName: "submariner-cable-remote-192-68-1-2",
			ClusterID: "remote",
			PrivateIP: "192.68.1.2",
			Hostname:  remoteNodeName,
			Subnets:   []string{remoteSubnet1, remoteSubnet2},
			Backend:   "libreswan",
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
