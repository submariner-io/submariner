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

package ovn_test

import (
	"context"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	libovsdbclient "github.com/ovn-org/libovsdb/client"
	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/nbdb"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/event/testing"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	fakePF "github.com/submariner-io/submariner/pkg/packetfilter/fake"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/ovn"
	fakeovn "github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/ovn/fake"
	"github.com/vishvananda/netlink"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8snet "k8s.io/utils/net"
	"k8s.io/utils/ptr"
)

const (
	ipv4ClusterCIDR      = "171.0.1.0/24"
	ipv4serviceCIDR      = "181.0.1.0/24"
	ipv6ClusterCIDR      = "c000:100::/64"
	ipv6serviceCIDR      = "d000:100::/64"
	ipv4OVNK8sMgmntIntGw = "100.1.1.1"
	ipv6OVNK8sMgmntIntGw = "b000:100::"
)

var _ = Describe("Handler", func() {
	ipv4Subnets := []string{"192.0.1.0/24", "192.0.2.0/24", "192.0.3.0/24"}
	ipv6Subnets := []string{"fc00:100::/64", "fd00:100::/64", "fe00:100::/64"}

	t := &handlerTestDriver{testDriver: newTestDriver()}

	BeforeEach(func() {
		t.ipFamily = k8snet.IPv4
	})

	JustBeforeEach(func() {
		t.ovsdbClient = fakeovn.NewOVSDBClient()

		_, _ = t.ovsdbClient.Create(&nbdb.LogicalRouter{
			Name: ovn.OVNClusterRouter,
		})

		t.netLink.SetupDefaultGateway(t.ipFamily, net.Interface{Name: "gw-intf"})

		t.pFilter = fakePF.New(t.ipFamily)

		if t.ipFamily == k8snet.IPv4 {
			t.clusterCIDR = ipv4ClusterCIDR
			t.serviceCIDR = ipv4serviceCIDR
			t.OVNK8sMgmntIntGw = ipv4OVNK8sMgmntIntGw
		} else {
			t.clusterCIDR = ipv6ClusterCIDR
			t.serviceCIDR = ipv6serviceCIDR
			t.OVNK8sMgmntIntGw = ipv6OVNK8sMgmntIntGw
		}

		_, err := t.k8sClient.CoreV1().Pods(testing.Namespace).Create(context.Background(), &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "ovn-pod",
				Labels: map[string]string{"app": "ovnkube-node"},
			},
		}, metav1.CreateOptions{})
		Expect(err).To(Succeed())

		Expect(t.netLink.RouteAdd(&netlink.Route{
			LinkIndex: OVNK8sMgmntIntIndex,
			Family:    netlinkAPI.ToNetlinkFamily(t.ipFamily),
			Dst:       toIPNet(t.clusterCIDR),
			Gw:        net.ParseIP(t.OVNK8sMgmntIntGw),
		})).To(Succeed())

		restMapper := test.GetRESTMapperFor(&submarinerv1.GatewayRoute{}, &submarinerv1.NonGatewayRoute{})

		transitSwitchIP := ovn.NewTransitSwitchIP(t.ipFamily)

		t.handler = ovn.NewHandler(t.ipFamily, &ovn.HandlerConfig{
			Namespace:   testing.Namespace,
			ClusterCIDR: []string{t.clusterCIDR},
			ServiceCIDR: []string{t.serviceCIDR},
			SubmClient:  t.submClient,
			K8sClient:   t.k8sClient,
			DynClient:   t.dynClient,
			WatcherConfig: &watcher.Config{
				RestMapper: restMapper,
				Client:     t.dynClient,
			},
			NewOVSDBClient: func(_ model.ClientDBModel, _ ...libovsdbclient.Option) (libovsdbclient.Client, error) {
				return t.ovsdbClient, nil
			},
			TransitSwitchIP: transitSwitchIP,
		})

		t.Start(t.handler)

		Expect(t.ovsdbClient.Connected()).To(BeTrue())
	})

	Context("IPv4", func() {
		t.testRemoteEndpoint(ipv4Subnets, ipv6Subnets)
		t.testGatewayTransitions(ipv4Subnets, ipv6Subnets)
		t.testGatewayRoute(ipv4Subnets, ipv6OVNK8sMgmntIntGw, ipv6Subnets)
		t.testNonGatewayRoutes(ipv4OVNK8sMgmntIntGw, ipv4Subnets, []string{"172.0.1.0/24"}, ipv6OVNK8sMgmntIntGw, ipv6Subnets)
	})

	Context("IPv6", func() {
		BeforeEach(func() {
			t.ipFamily = k8snet.IPv6
		})

		t.testRemoteEndpoint(ipv6Subnets, ipv4Subnets)
		t.testGatewayTransitions(ipv6Subnets, ipv4Subnets)
		t.testGatewayRoute(ipv6Subnets, ipv4OVNK8sMgmntIntGw, ipv4Subnets)
		t.testNonGatewayRoutes(ipv6OVNK8sMgmntIntGw, ipv6Subnets, []string{"ab00:100::/64"}, ipv4OVNK8sMgmntIntGw, ipv4Subnets)
	})

	When("the OVN management interface address changes", func() {
		JustBeforeEach(func() {
			t.CreateLocalHostEndpoint()
			t.netLink.AwaitGwRoutes(0, constants.RouteAgentInterClusterNetworkTableID, t.OVNK8sMgmntIntGw)

			t.createEndpoint("192.0.1.0/24")
			t.netLink.AwaitGwRoutes(0, constants.RouteAgentHostNetworkTableID, t.OVNK8sMgmntIntGw)
		})

		It("should update the gateway and host network dataplanes", func() {
			Expect(t.netLink.FlushRouteTable(constants.RouteAgentInterClusterNetworkTableID)).To(Succeed())
			Expect(t.netLink.FlushRouteTable(constants.RouteAgentHostNetworkTableID)).To(Succeed())

			link, err := t.netLink.LinkByName(ovn.OVNK8sMgmntIntfName)
			Expect(err).To(Succeed())

			Expect(t.netLink.AddrDel(link, &netlink.Addr{
				IPNet: t.OVNK8sMgmntIntCIDR[k8snet.IPv4],
			})).To(Succeed())

			t.OVNK8sMgmntIntCIDR[k8snet.IPv4] = toIPNet("128.2.30.3/24")
			Expect(t.netLink.AddrAdd(link, &netlink.Addr{
				IPNet: t.OVNK8sMgmntIntCIDR[k8snet.IPv4],
			})).To(Succeed())

			t.netLink.AwaitGwRoutes(0, constants.RouteAgentInterClusterNetworkTableID, t.OVNK8sMgmntIntGw)
			t.netLink.AwaitGwRoutes(0, constants.RouteAgentHostNetworkTableID, t.OVNK8sMgmntIntGw)
		})
	})

	Context("on Uninstall", func() {
		It("should delete the table rules", func() {
			Expect(t.pFilter.ChainExists(packetfilter.TableTypeFilter, ovn.ForwardingSubmarinerFWDChain)).To(BeTrue())
			Expect(t.pFilter.ChainExists(packetfilter.TableTypeFilter, ovn.ForwardingSubmarinerMSSClampChain)).To(BeTrue())

			_ = t.netLink.RuleAdd(&netlink.Rule{
				Table:  constants.RouteAgentHostNetworkTableID,
				Family: netlink.FAMILY_V4,
			})

			_ = t.netLink.RuleAdd(&netlink.Rule{
				Table:  constants.RouteAgentInterClusterNetworkTableID,
				Family: netlink.FAMILY_V4,
			})

			Expect(t.handler.Uninstall()).To(Succeed())

			t.netLink.AwaitNoRule(constants.RouteAgentHostNetworkTableID, "", "")
			t.netLink.AwaitNoRule(constants.RouteAgentInterClusterNetworkTableID, "", "")

			Expect(t.pFilter.ChainExists(packetfilter.TableTypeFilter, ovn.ForwardingSubmarinerFWDChain)).To(BeFalse())
		})
	})
})

type handlerTestDriver struct {
	*testDriver
	handler          event.Handler
	pFilter          *fakePF.PacketFilter
	ipFamily         k8snet.IPFamily
	clusterCIDR      string
	serviceCIDR      string
	OVNK8sMgmntIntGw string
	ovsdbClient      *fakeovn.OVSDBClient
}

func (t *handlerTestDriver) Start(handler event.Handler) {
	t.ControllerSupport.Start(handler)
	t.CreateNode(t.node)
}

//nolint:gocognit // Ignore "cognitive complexity ... is high".
func (t *handlerTestDriver) testRemoteEndpoint(ipFamilySubnets, nonIPFamilySubnets []string) {
	var (
		newEndpointSubnet string
		endpointSubnets   []string
	)

	BeforeEach(func() {
		newEndpointSubnet = ipFamilySubnets[len(ipFamilySubnets)-1]
		endpointSubnets = ipFamilySubnets[:len(ipFamilySubnets)-1]
	})

	When("a remote Endpoint is created, updated, and deleted", func() {
		It("should correctly update the host network dataplane", func() {
			By("Creating remote Endpoint")

			endpoint := t.createEndpoint(append(endpointSubnets, nonIPFamilySubnets...)...)

			for _, s := range endpointSubnets {
				t.netLink.AwaitRule(constants.RouteAgentHostNetworkTableID, "", s)
				t.netLink.EnsureNoRule(constants.RouteAgentInterClusterNetworkTableID, s, t.clusterCIDR)
				t.netLink.EnsureNoRule(constants.RouteAgentInterClusterNetworkTableID, s, t.serviceCIDR)
			}

			for _, s := range nonIPFamilySubnets {
				t.netLink.EnsureNoRule(constants.RouteAgentHostNetworkTableID, "", s)
			}

			t.netLink.AwaitGwRoutes(0, constants.RouteAgentHostNetworkTableID, t.OVNK8sMgmntIntGw)

			By("Updating remote Endpoint")

			oldSubnets := endpointSubnets
			endpointSubnets = []string{newEndpointSubnet}

			//nolint:gocritic // Ignore "append result not assigned to the same slice"
			endpoint.Spec.Subnets = append(endpointSubnets, nonIPFamilySubnets...)

			t.UpdateEndpoint(endpoint)

			for _, s := range oldSubnets {
				t.netLink.AwaitNoRule(constants.RouteAgentHostNetworkTableID, "", s)
			}

			for _, s := range endpointSubnets {
				t.netLink.AwaitRule(constants.RouteAgentHostNetworkTableID, "", s)
			}

			By("Deleting remote Endpoint")

			t.DeleteEndpoint(endpoint.Name)

			for _, s := range endpointSubnets {
				t.netLink.AwaitNoRule(constants.RouteAgentHostNetworkTableID, "", s)
			}
		})

		Context("on the gateway", func() {
			JustBeforeEach(func() {
				t.CreateLocalHostEndpoint()
			})

			It("should correctly update the gateway dataplane", func() {
				By("Creating remote Endpoint")

				endpoint := t.createEndpoint(append(endpointSubnets, nonIPFamilySubnets...)...)

				for _, s := range endpointSubnets {
					t.netLink.AwaitRule(constants.RouteAgentInterClusterNetworkTableID, s, t.clusterCIDR)
					t.netLink.AwaitRule(constants.RouteAgentInterClusterNetworkTableID, s, t.serviceCIDR)

					t.pFilter.AwaitRule(packetfilter.TableTypeNAT, constants.SmPostRoutingChain, ContainSubstring("\"SrcCIDR\":%q", s))
					t.pFilter.AwaitRule(packetfilter.TableTypeNAT, constants.SmPostRoutingChain, ContainSubstring("\"DestCIDR\":%q", s))
				}

				t.awaitOVNKNodeAnnotationContaining(endpointSubnets...)

				By("Updating remote Endpoint")

				oldSubnets := endpointSubnets
				endpointSubnets = []string{oldSubnets[0], newEndpointSubnet}

				//nolint:gocritic // Ignore "append result not assigned to the same slice"
				endpoint.Spec.Subnets = append(endpointSubnets, nonIPFamilySubnets...)

				t.UpdateEndpoint(endpoint)

				for i := 1; i < len(oldSubnets); i++ {
					t.netLink.AwaitNoRule(constants.RouteAgentInterClusterNetworkTableID, oldSubnets[i], t.clusterCIDR)
					t.netLink.AwaitNoRule(constants.RouteAgentInterClusterNetworkTableID, oldSubnets[i], t.serviceCIDR)
				}

				for _, s := range endpointSubnets {
					t.netLink.AwaitRule(constants.RouteAgentInterClusterNetworkTableID, s, t.clusterCIDR)
					t.netLink.AwaitRule(constants.RouteAgentInterClusterNetworkTableID, s, t.serviceCIDR)
				}

				By("Deleting remote Endpoint")

				t.DeleteEndpoint(endpoint.Name)

				for _, s := range endpointSubnets {
					t.netLink.AwaitNoRule(constants.RouteAgentInterClusterNetworkTableID, s, t.clusterCIDR)
					t.netLink.AwaitNoRule(constants.RouteAgentInterClusterNetworkTableID, s, t.serviceCIDR)

					t.pFilter.AwaitNoRule(packetfilter.TableTypeNAT, constants.SmPostRoutingChain, ContainSubstring("\"SrcCIDR\":%q", s))
					t.pFilter.AwaitNoRule(packetfilter.TableTypeNAT, constants.SmPostRoutingChain, ContainSubstring("\"DestCIDR\":%q", s))
				}

				// Since we updated the subnets above, the original second one will remain b/c the annotation isn't currently
				// updated on an Endpoint update.
				t.awaitOVNKNodeAnnotationContaining(oldSubnets[1])
			})
		})
	})
}

func (t *handlerTestDriver) testGatewayTransitions(ipFamilySubnets, nonIPFamilySubnets []string) {
	Context("on gateway transitions", func() {
		It("should correctly update the gateway dataplane", func() {
			t.createEndpoint(append(ipFamilySubnets, nonIPFamilySubnets...)...)

			By("Creating local gateway Endpoint")

			localEP := t.CreateLocalHostEndpoint()

			for _, s := range ipFamilySubnets {
				t.netLink.AwaitRule(constants.RouteAgentInterClusterNetworkTableID, s, t.clusterCIDR)
				t.netLink.AwaitRule(constants.RouteAgentInterClusterNetworkTableID, s, t.serviceCIDR)
			}

			for _, s := range nonIPFamilySubnets {
				t.netLink.EnsureNoRule(constants.RouteAgentInterClusterNetworkTableID, s, t.clusterCIDR)
			}

			t.awaitOVNKNodeAnnotationContaining(ipFamilySubnets...)

			t.netLink.AwaitGwRoutes(0, constants.RouteAgentInterClusterNetworkTableID, t.OVNK8sMgmntIntGw)

			By("Deleting local gateway Endpoint")

			t.DeleteEndpoint(localEP.Name)

			for _, s := range ipFamilySubnets {
				t.netLink.AwaitNoRule(constants.RouteAgentInterClusterNetworkTableID, s, t.clusterCIDR)
				t.netLink.AwaitNoRule(constants.RouteAgentInterClusterNetworkTableID, s, t.serviceCIDR)
			}

			t.awaitOVNKNodeAnnotationContaining()

			t.netLink.AwaitNoGwRoutes(0, constants.RouteAgentInterClusterNetworkTableID, t.OVNK8sMgmntIntGw)
		})
	})
}

func (t *handlerTestDriver) testGatewayRoute(ipFamilySubnets []string, nonIPFamilyNextHop string, nonIPFamilySubnets []string) {
	When("a GatewayRoute is created and deleted", func() {
		It("should correctly reconcile OVN router policies", func() {
			client := t.dynClient.Resource(submarinerv1.SchemeGroupVersion.WithResource("gatewayroutes")).Namespace(testing.Namespace)

			gwRoute := &submarinerv1.GatewayRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-gateway-route",
				},
				RoutePolicySpec: submarinerv1.RoutePolicySpec{
					NextHops:    []string{t.OVNK8sMgmntIntCIDR[t.ipFamily].IP.String()},
					RemoteCIDRs: ipFamilySubnets,
				},
			}

			test.CreateResource(client, gwRoute)

			for _, cidr := range gwRoute.RoutePolicySpec.RemoteCIDRs {
				t.ovsdbClient.AwaitModel(&nbdb.LogicalRouterPolicy{
					Match:   cidr,
					Nexthop: ptr.To(gwRoute.RoutePolicySpec.NextHops[0]),
				})

				t.ovsdbClient.AwaitModel(&nbdb.LogicalRouterStaticRoute{
					IPPrefix: cidr,
				})
			}

			Expect(client.Delete(context.Background(), gwRoute.Name, metav1.DeleteOptions{})).To(Succeed())

			for _, cidr := range gwRoute.RoutePolicySpec.RemoteCIDRs {
				t.ovsdbClient.AwaitNoModel(&nbdb.LogicalRouterPolicy{
					Match:   cidr,
					Nexthop: ptr.To(gwRoute.RoutePolicySpec.NextHops[0]),
				})

				t.ovsdbClient.AwaitNoModel(&nbdb.LogicalRouterStaticRoute{
					IPPrefix: cidr,
				})
			}

			test.CreateResource(client, &submarinerv1.GatewayRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-gateway-route",
				},
				RoutePolicySpec: submarinerv1.RoutePolicySpec{
					NextHops:    []string{nonIPFamilyNextHop},
					RemoteCIDRs: nonIPFamilySubnets,
				},
			})
		})
	})
}

func (t *handlerTestDriver) testNonGatewayRoutes(ipFamilyNextHop string, ipFamilyCIDRs1, ipFamilyCIDRs2 []string, nonIPFamilyNextHop string,
	nonIPFamilyCIDRs []string,
) {
	When("NonGatewayRoutes are created, updated and deleted", func() {
		verifyLogicalRouterPolicies := func(ngr *submarinerv1.NonGatewayRoute, nextHop string) {
			for _, cidr := range ngr.RoutePolicySpec.RemoteCIDRs {
				t.ovsdbClient.AwaitModel(&nbdb.LogicalRouterPolicy{
					Match:   cidr,
					Nexthop: ptr.To(nextHop),
				})
			}
		}

		verifyNoLogicalRouterPolicies := func(ngr *submarinerv1.NonGatewayRoute, nextHop string) {
			for _, cidr := range ngr.RoutePolicySpec.RemoteCIDRs {
				t.ovsdbClient.AwaitNoModel(&nbdb.LogicalRouterPolicy{
					Match:   cidr,
					Nexthop: ptr.To(nextHop),
				})
			}
		}

		It("should correctly reconcile OVN router policies", func() {
			client := t.dynClient.Resource(submarinerv1.SchemeGroupVersion.WithResource("nongatewayroutes")).Namespace(testing.Namespace)

			By("Creating first NonGatewayRoute")

			nonGWRoute1 := &submarinerv1.NonGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-nongateway-route1",
				},
				RoutePolicySpec: submarinerv1.RoutePolicySpec{
					NextHops:    []string{ipFamilyNextHop},
					RemoteCIDRs: ipFamilyCIDRs1,
				},
			}

			test.CreateResource(client, nonGWRoute1)

			verifyLogicalRouterPolicies(nonGWRoute1, ipFamilyNextHop)

			By("Creating second NonGatewayRoute")

			nonGWRoute2 := &submarinerv1.NonGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-nongateway-route2",
				},
				RoutePolicySpec: submarinerv1.RoutePolicySpec{
					NextHops:    []string{ipFamilyNextHop},
					RemoteCIDRs: ipFamilyCIDRs2,
				},
			}

			test.CreateResource(client, nonGWRoute2)

			verifyLogicalRouterPolicies(nonGWRoute1, ipFamilyNextHop)
			verifyLogicalRouterPolicies(nonGWRoute2, ipFamilyNextHop)

			By("Updating NextHop for first NonGatewayRoute")

			prevNextHop := ipFamilyNextHop

			newIP := net.ParseIP(prevNextHop)
			newIP[len(newIP)-1]++
			ipFamilyNextHop = newIP.String()

			nonGWRoute1.RoutePolicySpec.NextHops[0] = ipFamilyNextHop

			test.UpdateResource(client, nonGWRoute1)

			verifyLogicalRouterPolicies(nonGWRoute1, ipFamilyNextHop)
			verifyNoLogicalRouterPolicies(nonGWRoute1, prevNextHop)
			verifyNoLogicalRouterPolicies(nonGWRoute2, prevNextHop)

			By("Updating NextHop for second NonGatewayRoute")

			nonGWRoute2.RoutePolicySpec.NextHops[0] = ipFamilyNextHop

			test.UpdateResource(client, nonGWRoute2)

			verifyLogicalRouterPolicies(nonGWRoute1, ipFamilyNextHop)
			verifyLogicalRouterPolicies(nonGWRoute2, ipFamilyNextHop)

			By("Deleting first NonGatewayRoute")

			Expect(client.Delete(context.Background(), nonGWRoute1.Name, metav1.DeleteOptions{})).To(Succeed())

			verifyNoLogicalRouterPolicies(nonGWRoute1, ipFamilyNextHop)

			By("Creating NonGatewayRoute for other IP family")

			test.CreateResource(client, &submarinerv1.NonGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-nongateway-route-other",
				},
				RoutePolicySpec: submarinerv1.RoutePolicySpec{
					NextHops:    []string{nonIPFamilyNextHop},
					RemoteCIDRs: nonIPFamilyCIDRs,
				},
			})

			for _, cidr := range nonIPFamilyCIDRs {
				t.ovsdbClient.EnsureNoModel(&nbdb.LogicalRouterPolicy{
					Match:   cidr,
					Nexthop: ptr.To(nonIPFamilyNextHop),
				})
			}
		})
	})
}
