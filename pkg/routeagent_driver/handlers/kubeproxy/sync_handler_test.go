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
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
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
	"k8s.io/klog"
)

const (
	localClusterCIDR = "169.254.1.0/24"
	localServiceCIDR = "169.254.2.0/24"
	remoteSubnet1    = "170.250.1.0/24"
	remoteSubnet2    = "171.250.1.0/24"
	localNodeName1   = "local-node1"
	// localNodeName2   = "local-node2".
	remoteNodeName = "remote-node"
	nodeAddress1   = "10.253.10.2"
	nodeAddress2   = "10.253.10.3"
	cniIPAddress   = "192.168.5.1"
)

var _ = Describe("SyncHandler", func() {
	Describe("Endpoints", testEndpoints)
	Describe("Gateway transition", testGatewayTransition)
	Describe("Nodes", testNodes)
})

func testEndpoints() {
	t := newTestDriver()

	When("a local Endpoint is created while on a non-gateway node", func() {
		JustBeforeEach(func() {
			Expect(t.handler.LocalEndpointCreated(t.localEndpoint)).To(Succeed())
		})

		It("should add the VxLAN interface", func() {
			Expect(toVxlan(t.netLink.AwaitLink(kubeproxy.VxLANIface)).SrcAddr).To(Equal(getVxlanVtepIPAddress(t.hostInterfaceIP.String())))
		})

		Context("and old VxLAN routes are present", func() {
			BeforeEach(func() {
				t.addVxLANRoute(remoteSubnet1)
			})

			It("should remove them", func() {
				t.netLink.AwaitNoRoutes(t.vxLanInterfaceIndex, remoteSubnet1)
			})
		})

		Context("and remote subnets are present", func() {
			BeforeEach(func() {
				Expect(t.handler.RemoteEndpointCreated(t.remoteEndpoint)).To(Succeed())
			})

			It("should add VxLAN routes for the remote subnets", func() {
				t.verifyVxLANRoutes()
			})
		})

		It("should add an FDB entry on the VxLAN interface for the endpoint address", func() {
			t.netLink.AwaitNeighbors(t.vxLanInterfaceIndex, t.localEndpoint.Spec.PrivateIP)
		})
	})

	When("a local Endpoint is removed while on a non-gateway node", func() {
		BeforeEach(func() {
			Expect(t.handler.LocalEndpointCreated(t.localEndpoint)).To(Succeed())
			t.netLink.AwaitLink(kubeproxy.VxLANIface)
		})

		JustBeforeEach(func() {
			Expect(t.handler.LocalEndpointRemoved(t.localEndpoint)).To(Succeed())
		})

		It("should remove an FDB entry on the VxLAN interface for the endpoint address", func() {
			t.netLink.AwaitNoNeighbors(t.vxLanInterfaceIndex, t.localEndpoint.Spec.PrivateIP)
		})
	})

	When("a local Endpoint is created while on a gateway node", func() {
		It("should not add an FDB entry for the local Endpoint", func() {
			t.netLink.AwaitNoNeighbors(t.vxLanInterfaceIndex, t.localEndpoint.Spec.PrivateIP)
		})
	})

	When("a remote Endpoint is created while on a non-gateway node", func() {
		JustBeforeEach(func() {
			Expect(t.handler.RemoteEndpointCreated(t.remoteEndpoint)).To(Succeed())
		})

		Context("after a local Endpoint was created", func() {
			BeforeEach(func() {
				Expect(t.handler.LocalEndpointCreated(t.localEndpoint)).To(Succeed())
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

			It("should not add an FDB entry for the remote Endpoint", func() {
				t.netLink.AwaitNoNeighbors(t.vxLanInterfaceIndex, t.remoteEndpoint.Spec.PrivateIP)
			})

			Context("and is subsequently removed", func() {
				JustBeforeEach(func() {
					Expect(t.handler.RemoteEndpointRemoved(t.remoteEndpoint)).To(Succeed())
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
				Expect(t.handler.RemoteEndpointRemoved(t.remoteEndpoint)).To(Succeed())
				Expect(t.handler.LocalEndpointCreated(t.localEndpoint)).To(Succeed())
			})

			It("should not add VxLAN routes for the remote subnets", func() {
				t.verifyNoVxLANRoutes()
			})

			It("should not add an FDB entry for the remote Endpoint", func() {
				t.netLink.AwaitNoNeighbors(t.vxLanInterfaceIndex, t.remoteEndpoint.Spec.PrivateIP)
			})
		})
	})

	When("a remote Endpoint is created while on a gateway node", func() {
		JustBeforeEach(func() {
			Expect(t.handler.TransitionToGateway()).To(Succeed())
			Expect(t.handler.RemoteEndpointCreated(t.remoteEndpoint)).To(Succeed())
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

		It("should remove an FDB entry on the VxLAN interface for the local endpoint address", func() {
			t.netLink.AwaitNoNeighbors(t.vxLanInterfaceIndex, t.localEndpoint.Spec.PrivateIP)
		})

		Context("and is subsequently removed", func() {
			JustBeforeEach(func() {
				Expect(t.handler.RemoteEndpointRemoved(t.remoteEndpoint)).To(Succeed())
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
		JustBeforeEach(func() {
			Expect(t.handler.RemoteEndpointCreated(t.remoteEndpoint)).To(Succeed())
			Expect(t.handler.TransitionToGateway()).To(Succeed())
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

		It("should remove an FDB entry on the VxLAN interface for the local endpoint address", func() {
			t.netLink.AwaitNoNeighbors(t.vxLanInterfaceIndex, t.localEndpoint.Spec.PrivateIP)
		})

		Context("and previous VxLAN routes are present", func() {
			BeforeEach(func() {
				t.addVxLANRoute(remoteSubnet1)
				Expect(t.handler.LocalEndpointCreated(t.localEndpoint)).To(Succeed())
			})

			It("should remove them", func() {
				t.netLink.AwaitNoRoutes(t.vxLanInterfaceIndex, remoteSubnet1)
			})
		})

		Context("and Node addresses are present", func() {
			BeforeEach(func() {
				Expect(t.handler.NodeCreated(newNode(nodeAddress1))).To(Succeed())
				Expect(t.handler.NodeCreated(newNode(nodeAddress2))).To(Succeed())
			})

			It("should add an FDB entry on the VxLAN interface for each address", func() {
				t.netLink.AwaitNeighbors(t.vxLanInterfaceIndex, nodeAddress1, nodeAddress2)
			})
		})

		Context("and then to non-gateway", func() {
			JustBeforeEach(func() {
				Expect(t.handler.TransitionToNonGateway()).To(Succeed())
				Expect(t.handler.LocalEndpointCreated(t.localEndpoint)).To(Succeed())
			})

			It("should remove the routing rule for the RouteAgentHostNetworkTableID", func() {
				t.netLink.AwaitNoRule(constants.RouteAgentHostNetworkTableID)
			})

			It("should remove host networking routing rules for the remote subnets", func() {
				t.verifyNoHostNetworkingRoutes()
			})

			It("should add an FDB entry on the VxLAN interface for the local endpoint address", func() {
				t.netLink.AwaitNeighbors(t.vxLanInterfaceIndex, t.localEndpoint.Spec.PrivateIP)
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

	When("a Node is created on a gateway node", func() {
		JustBeforeEach(func() {
			Expect(t.handler.TransitionToGateway()).To(Succeed())
			Expect(t.handler.NodeCreated(node)).To(Succeed())
		})

		It("should add an FDB entry on the VxLAN interface for each Node address", func() {
			t.netLink.AwaitNeighbors(t.vxLanInterfaceIndex, nodeAddress1)
		})

		Context("and then is removed", func() {
			JustBeforeEach(func() {
				Expect(t.handler.NodeRemoved(node)).To(Succeed())
			})

			It("should remove the FDB entry on the VxLAN interface for each Node address", func() {
				t.netLink.AwaitNoNeighbors(t.vxLanInterfaceIndex, nodeAddress1)
			})
		})
	})

	When("a Node is created on a non-gateway node", func() {
		JustBeforeEach(func() {
			Expect(t.handler.NodeCreated(node)).To(Succeed())
		})

		It("should not add an FDB entry on the VxLAN interface for each Node address", func() {
			t.netLink.AwaitNoNeighbors(t.vxLanInterfaceIndex, nodeAddress1)
		})
	})

	When("a GW Node is created on a non-gateway node", func() {
		JustBeforeEach(func() {
			node.Labels = map[string]string{"submariner.io/gateway": "true"}
			// Now the localnode address is the endpoint private ip
			t.localEndpoint.Spec.PrivateIP = nodeAddress1
			Expect(t.handler.NodeCreated(node)).To(Succeed())
			// An endpoint will be created once the GW node is added
			Expect(t.handler.LocalEndpointCreated(t.localEndpoint)).To(Succeed())
		})

		It("should not add an FDB entry on the VxLAN interface for each Node address", func() {
			t.netLink.AwaitNeighbors(t.vxLanInterfaceIndex, nodeAddress1)
		})
	})
}

type testDriver struct {
	handler             *kubeproxy.SyncHandler
	ipTables            *fakeIPT.IPTables
	netLink             *fakeNetlink.NetLink
	localEndpoint       *submarinerv1.Endpoint
	remoteEndpoint      *submarinerv1.Endpoint
	hostInterfaceIP     net.IP
	hostInterfaceIndex  int
	vxLanInterfaceIndex int
}

func newTestDriver() *testDriver {
	t := &testDriver{}

	BeforeEach(func() {
		defaultHostIface, err := netlinkAPI.GetDefaultGatewayInterface()
		Expect(err).To(Succeed())

		t.hostInterfaceIndex = defaultHostIface.Index
		addrs, err := defaultHostIface.Addrs()
		Expect(err).To(Succeed())

		t.hostInterfaceIP, _, err = net.ParseCIDR(addrs[0].String())
		Expect(err).To(Succeed())

		klog.Infof("Parsed host Interface IP %v", t.hostInterfaceIP)

		t.vxLanInterfaceIndex = t.hostInterfaceIndex + 1

		t.netLink = fakeNetlink.New()
		t.netLink.SetLinkIndex(kubeproxy.VxLANIface, t.vxLanInterfaceIndex)

		netlinkAPI.NewFunc = func() netlinkAPI.Interface {
			return t.netLink
		}

		// // Configure Fake link to have node IP 		// nolint:gocritic // TODO MAG POC
		// ipConfig := &netlink.Addr{IPNet: &net.IPNet{
		// 	IP:   net.ParseIP(vetpNodeAddress1),
		// 	Mask: net.CIDRMask(8, 32),
		// }}

		// link, err := netlink.LinkByName(kubeproxy.VxLANIface) // nolint:gocritic // TODO MAG POC

		// Expect(err).To(Succeed()) // nolint:gocritic // TODO MAG POC
		// err = t.netLink.AddrAdd(link, ipConfig)
		// Expect(err).To(Succeed())

		// netlinkAPI.NewFunc = func() netlinkAPI.Interface { // nolint:gocritic // TODO MAG POC
		// 	return t.netLink
		// }

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

		t.localEndpoint = newLocalEndpoint()
		t.remoteEndpoint = newRemoteEndpoint()

		t.handler = kubeproxy.NewSyncHandler([]string{localClusterCIDR}, []string{localServiceCIDR}, false)
		Expect(t.handler.Init()).To(Succeed())
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

func newLocalEndpoint() *submarinerv1.Endpoint {
	return &submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cable-local",
		},
		Spec: submarinerv1.EndpointSpec{
			CableName: "submariner-cable-local-192-68-1-2",
			ClusterID: "local",
			PrivateIP: "192.68.1.3",
			Hostname:  localNodeName1,
			Backend:   "libreswan",
		},
	}
}

func newRemoteEndpoint() *submarinerv1.Endpoint {
	return &submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cable-remote",
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
			Name: "some-node",
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

func getVxlanVtepIPAddress(ipAddr string) net.IP {
	ipSlice := strings.Split(ipAddr, ".")
	if len(ipSlice) < 4 {
		Fail(fmt.Sprintf("invalid ipAddr [%s]", ipAddr))
	}

	ipSlice[0] = strconv.Itoa(kubeproxy.VxLANVTepNetworkPrefix)
	vxlanIP := net.ParseIP(strings.Join(ipSlice, "."))

	return vxlanIP
}
