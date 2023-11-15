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
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	libovsdbclient "github.com/ovn-org/libovsdb/client"
	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/nbdb"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event/testing"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/ovn"
	fakeovn "github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/ovn/fake"
	"github.com/vishvananda/netlink"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

const (
	clusterCIDR      = "171.0.1.0/24"
	serviceCIDR      = "181.0.1.0/24"
	OVNK8sMgmntIntGw = "100.1.1.1"
)

var _ = Describe("Handler", func() {
	t := newTestDriver()

	var ovsdbClient *fakeovn.OVSDBClient

	BeforeEach(func() {
		ovsdbClient = fakeovn.NewOVSDBClient()

		_, _ = ovsdbClient.Create(&nbdb.LogicalRouter{
			Name: ovn.OVNClusterRouter,
		})
	})

	JustBeforeEach(func() {
		_, err := t.k8sClient.CoreV1().Pods(testing.Namespace).Create(context.Background(), &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "ovn-pod",
				Labels: map[string]string{"app": "ovnkube-node"},
			},
		}, metav1.CreateOptions{})
		Expect(err).To(Succeed())

		Expect(t.netLink.RouteAdd(&netlink.Route{
			LinkIndex: OVNK8sMgmntIntIndex,
			Family:    syscall.AF_INET,
			Dst:       toIPNet(clusterCIDR),
			Gw:        net.ParseIP(OVNK8sMgmntIntGw),
		})).To(Succeed())

		restMapper := test.GetRESTMapperFor(&submarinerv1.GatewayRoute{}, &submarinerv1.NonGatewayRoute{})

		t.Start(ovn.NewHandler(&ovn.HandlerConfig{
			Namespace:   testing.Namespace,
			ClusterCIDR: []string{clusterCIDR},
			ServiceCIDR: []string{serviceCIDR},
			SubmClient:  t.submClient,
			K8sClient:   t.k8sClient,
			DynClient:   t.dynClient,
			WatcherConfig: &watcher.Config{
				RestMapper: restMapper,
				Client:     t.dynClient,
			},
			NewOVSDBClient: func(_ model.ClientDBModel, opts ...libovsdbclient.Option) (libovsdbclient.Client, error) {
				return ovsdbClient, nil
			},
		}))

		Expect(ovsdbClient.Connected()).To(BeTrue())
	})

	When("a remote Endpoint is created, updated, and deleted", func() {
		It("should correctly update the host network dataplane", func() {
			By("Creating remote Endpoint")

			endpoint := t.CreateEndpoint(testing.NewEndpoint("remote-cluster", "host", "192.0.1.0/24", "192.0.2.0/24"))

			for _, s := range endpoint.Spec.Subnets {
				t.netLink.AwaitRule(constants.RouteAgentHostNetworkTableID, "", s)
				t.netLink.AwaitNoRule(constants.RouteAgentInterClusterNetworkTableID, s, clusterCIDR)
				t.netLink.AwaitNoRule(constants.RouteAgentInterClusterNetworkTableID, s, serviceCIDR)
			}

			t.netLink.AwaitGwRoutes(0, constants.RouteAgentHostNetworkTableID, OVNK8sMgmntIntGw)

			By("Updating remote Endpoint")

			oldSubnets := endpoint.Spec.Subnets
			endpoint.Spec.Subnets = []string{"192.0.3.0/24"}
			t.UpdateEndpoint(endpoint)

			for _, s := range oldSubnets {
				t.netLink.AwaitNoRule(constants.RouteAgentHostNetworkTableID, "", s)
			}

			for _, s := range endpoint.Spec.Subnets {
				t.netLink.AwaitRule(constants.RouteAgentHostNetworkTableID, "", s)
			}

			By("Deleting remote Endpoint")

			t.DeleteEndpoint(endpoint.Name)

			for _, s := range endpoint.Spec.Subnets {
				t.netLink.AwaitNoRule(constants.RouteAgentHostNetworkTableID, "", s)
			}
		})

		Context("on the gateway", func() {
			JustBeforeEach(func() {
				t.CreateLocalHostEndpoint()
			})

			It("should correctly update the gateway dataplane", func() {
				By("Creating remote Endpoint")

				endpoint := t.CreateEndpoint(testing.NewEndpoint("remote-cluster", "host", "192.0.1.0/24", "192.0.2.0/24"))

				for _, s := range endpoint.Spec.Subnets {
					t.netLink.AwaitRule(constants.RouteAgentInterClusterNetworkTableID, s, clusterCIDR)
					t.netLink.AwaitRule(constants.RouteAgentInterClusterNetworkTableID, s, serviceCIDR)
				}

				By("Updating remote Endpoint")

				oldSubnets := endpoint.Spec.Subnets
				endpoint.Spec.Subnets = []string{oldSubnets[0], "192.0.3.0/24"}
				t.UpdateEndpoint(endpoint)

				for i := 1; i < len(oldSubnets); i++ {
					t.netLink.AwaitNoRule(constants.RouteAgentInterClusterNetworkTableID, oldSubnets[i], clusterCIDR)
					t.netLink.AwaitNoRule(constants.RouteAgentInterClusterNetworkTableID, oldSubnets[i], serviceCIDR)
				}

				for _, s := range endpoint.Spec.Subnets {
					t.netLink.AwaitRule(constants.RouteAgentInterClusterNetworkTableID, s, clusterCIDR)
					t.netLink.AwaitRule(constants.RouteAgentInterClusterNetworkTableID, s, serviceCIDR)
				}

				By("Deleting remote Endpoint")

				t.DeleteEndpoint(endpoint.Name)

				for _, s := range endpoint.Spec.Subnets {
					t.netLink.AwaitNoRule(constants.RouteAgentInterClusterNetworkTableID, s, clusterCIDR)
					t.netLink.AwaitNoRule(constants.RouteAgentInterClusterNetworkTableID, s, serviceCIDR)
				}
			})
		})
	})

	Context("on gateway transitions", func() {
		It("should correctly update the gateway dataplane", func() {
			endpoint := t.CreateEndpoint(testing.NewEndpoint("remote-cluster", "host", "192.0.1.0/24", "192.0.2.0/24"))

			By("Creating local gateway Endpoint")

			localEP := t.CreateLocalHostEndpoint()

			for _, s := range endpoint.Spec.Subnets {
				t.netLink.AwaitRule(constants.RouteAgentInterClusterNetworkTableID, s, clusterCIDR)
				t.netLink.AwaitRule(constants.RouteAgentInterClusterNetworkTableID, s, serviceCIDR)
			}

			t.netLink.AwaitGwRoutes(0, constants.RouteAgentInterClusterNetworkTableID, OVNK8sMgmntIntGw)

			By("Deleting local gateway Endpoint")

			t.DeleteEndpoint(localEP.Name)

			for _, s := range endpoint.Spec.Subnets {
				t.netLink.AwaitNoRule(constants.RouteAgentInterClusterNetworkTableID, s, clusterCIDR)
				t.netLink.AwaitNoRule(constants.RouteAgentInterClusterNetworkTableID, s, serviceCIDR)
			}

			t.netLink.AwaitNoGwRoutes(0, constants.RouteAgentInterClusterNetworkTableID, OVNK8sMgmntIntGw)
		})
	})

	When("a GatewayRoute is created and deleted", func() {
		It("should correctly reconcile OVN router policies", func() {
			client := t.dynClient.Resource(submarinerv1.SchemeGroupVersion.WithResource("gatewayroutes")).Namespace(testing.Namespace)

			gwRoute := &submarinerv1.GatewayRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-gateway-route",
				},
				RoutePolicySpec: submarinerv1.RoutePolicySpec{
					NextHops:    []string{OVNK8sMgmntIntCIDR.IP.String()},
					RemoteCIDRs: []string{"192.0.1.0/24", "192.0.2.0/24"},
				},
			}

			test.CreateResource(client, gwRoute)

			for _, cidr := range gwRoute.RoutePolicySpec.RemoteCIDRs {
				ovsdbClient.AwaitModel(&nbdb.LogicalRouterPolicy{
					Match:   cidr,
					Nexthop: ptr.To(gwRoute.RoutePolicySpec.NextHops[0]),
				})

				ovsdbClient.AwaitModel(&nbdb.LogicalRouterStaticRoute{
					IPPrefix: cidr,
				})
			}

			Expect(client.Delete(context.Background(), gwRoute.Name, metav1.DeleteOptions{})).To(Succeed())

			for _, cidr := range gwRoute.RoutePolicySpec.RemoteCIDRs {
				ovsdbClient.AwaitNoModel(&nbdb.LogicalRouterPolicy{
					Match:   cidr,
					Nexthop: ptr.To(gwRoute.RoutePolicySpec.NextHops[0]),
				})

				ovsdbClient.AwaitNoModel(&nbdb.LogicalRouterStaticRoute{
					IPPrefix: cidr,
				})
			}
		})
	})

	When("a NonGatewayRoute is created and deleted", func() {
		It("should correctly reconcile OVN router policies", func() {
			client := t.dynClient.Resource(submarinerv1.SchemeGroupVersion.WithResource("nongatewayroutes")).Namespace(testing.Namespace)

			nonGWRoute := &submarinerv1.NonGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-nongateway-route",
				},
				RoutePolicySpec: submarinerv1.RoutePolicySpec{
					NextHops:    []string{"111.1.1.1"},
					RemoteCIDRs: []string{"192.0.1.0/24", "192.0.2.0/24"},
				},
			}

			test.CreateResource(client, nonGWRoute)

			for _, cidr := range nonGWRoute.RoutePolicySpec.RemoteCIDRs {
				ovsdbClient.AwaitModel(&nbdb.LogicalRouterPolicy{
					Match:   cidr,
					Nexthop: ptr.To(nonGWRoute.RoutePolicySpec.NextHops[0]),
				})
			}

			Expect(client.Delete(context.Background(), nonGWRoute.Name, metav1.DeleteOptions{})).To(Succeed())

			for _, cidr := range nonGWRoute.RoutePolicySpec.RemoteCIDRs {
				ovsdbClient.AwaitNoModel(&nbdb.LogicalRouterPolicy{
					Match:   cidr,
					Nexthop: ptr.To(nonGWRoute.RoutePolicySpec.NextHops[0]),
				})
			}
		})
	})

	When("the OVN management interface address changes", func() {
		JustBeforeEach(func() {
			t.CreateLocalHostEndpoint()
			t.netLink.AwaitGwRoutes(0, constants.RouteAgentInterClusterNetworkTableID, OVNK8sMgmntIntGw)

			t.CreateEndpoint(testing.NewEndpoint("remote-cluster", "host", "192.0.1.0/24"))
			t.netLink.AwaitGwRoutes(0, constants.RouteAgentHostNetworkTableID, OVNK8sMgmntIntGw)
		})

		It("should update the gateway and host network dataplanes", func() {
			Expect(t.netLink.FlushRouteTable(constants.RouteAgentInterClusterNetworkTableID)).To(Succeed())
			Expect(t.netLink.FlushRouteTable(constants.RouteAgentHostNetworkTableID)).To(Succeed())

			link, err := t.netLink.LinkByName(ovn.OVNK8sMgmntIntfName)
			Expect(err).To(Succeed())

			Expect(t.netLink.AddrDel(link, &netlink.Addr{
				IPNet: OVNK8sMgmntIntCIDR,
			})).To(Succeed())

			newMgmtIPNet := toIPNet("128.2.30.3/24")
			Expect(t.netLink.AddrAdd(link, &netlink.Addr{
				IPNet: newMgmtIPNet,
			})).To(Succeed())

			t.netLink.AwaitGwRoutes(0, constants.RouteAgentInterClusterNetworkTableID, OVNK8sMgmntIntGw)
			t.netLink.AwaitGwRoutes(0, constants.RouteAgentHostNetworkTableID, OVNK8sMgmntIntGw)
		})
	})

	Context("on Uninstall", func() {
		It("should delete the table rules", func() {
			Expect(t.ipTables.ChainExists(constants.FilterTable, ovn.ForwardingSubmarinerFWDChain)).To(BeTrue())
			Expect(t.ipTables.ChainExists(constants.FilterTable, ovn.ForwardingSubmarinerMSSClampChain)).To(BeTrue())

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

			Expect(t.ipTables.ChainExists(constants.FilterTable, ovn.ForwardingSubmarinerFWDChain)).To(BeFalse())
		})
	})
})
