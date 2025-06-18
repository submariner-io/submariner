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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/admiral/pkg/test"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event/testing"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/ovn"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8snet "k8s.io/utils/net"
)

var _ = Describe("GatewayRouteHandler", func() {
	ipv4Subnets := []string{"192.0.1.0/24", "193.0.1.0/24"}
	ipv6Subnets := []string{"ec00:100::/64", "fc00:200::/64"}

	t := &gwRouteHandlerTestDriver{newTestDriver()}

	Context("IPv4", func() {
		t.testRemoteEndpoints(k8snet.IPv4, ipv4Subnets, ipv6Subnets)
	})

	Context("IPv6", func() {
		t.testRemoteEndpoints(k8snet.IPv6, ipv6Subnets, ipv4Subnets)
	})

	Context("Dual-stack", func() {
		JustBeforeEach(func() {
			t.Start(ovn.NewGatewayRouteHandler(k8snet.IPv4, t.submClient), ovn.NewGatewayRouteHandler(k8snet.IPv6, t.submClient))

			t.CreateLocalHostEndpoint()
		})

		It("should create GatewayRoutes for IPv4 and IPv6", func() {
			t.createEndpoint(append(ipv6Subnets, ipv4Subnets...)...)

			t.awaitGatewayRoute(k8snet.IPv4, ipv4Subnets)
			t.awaitGatewayRoute(k8snet.IPv6, ipv6Subnets)
		})
	})

	Context("on transition to gateway", func() {
		JustBeforeEach(func() {
			t.Start(ovn.NewGatewayRouteHandler(k8snet.IPv4, t.submClient))
		})

		It("should create GatewayRoutes for all remote Endpoints", func() {
			t.createEndpoint(ipv4Subnets...)
			t.ensureNumGatewayRoutes(0)

			localEndpoint := t.CreateLocalHostEndpoint()
			t.awaitGatewayRoute(k8snet.IPv4, ipv4Subnets)

			t.DeleteEndpoint(localEndpoint.Name)

			t.submClient.Fake.ClearActions()
			t.CreateLocalHostEndpoint()

			test.EnsureNoActionsForResource(&t.submClient.Fake, "gatewayroutes", "create")
			t.awaitGatewayRoute(k8snet.IPv4, ipv4Subnets)
		})
	})
})

type gwRouteHandlerTestDriver struct {
	*testDriver
}

func (t *gwRouteHandlerTestDriver) testRemoteEndpoints(ipFamily k8snet.IPFamily, ipFamilySubnets, nonIPFamilySubnets []string) {
	var endpoint *submarinerv1.Endpoint

	JustBeforeEach(func() {
		t.Start(ovn.NewGatewayRouteHandler(ipFamily, t.submClient))
	})

	When("a remote Endpoint is created and deleted on the gateway", func() {
		JustBeforeEach(func() {
			t.CreateLocalHostEndpoint()

			By(fmt.Sprintf("Creating remote Endpoint with subnets %v", ipFamilySubnets))

			endpoint = t.createEndpoint(ipFamilySubnets...)
		})

		It("should create/delete GatewayRoutes", func() {
			gwRouteName := t.awaitGatewayRoute(ipFamily, ipFamilySubnets)

			t.CreateEndpoint(testing.NewEndpoint("other"+remoteClusterID, "host", nonIPFamilySubnets...))
			t.ensureNumGatewayRoutes(1)

			By("Deleting remote Endpoint")

			t.DeleteEndpoint(endpoint.Name)
			test.AwaitNoResource(ovn.GatewayResourceInterface(t.submClient, testing.Namespace), gwRouteName)

			By(fmt.Sprintf("Creating remote Endpoint with subnets %v", append(ipFamilySubnets, nonIPFamilySubnets...)))

			t.createEndpoint(append(ipFamilySubnets, nonIPFamilySubnets...)...)
			t.awaitGatewayRoute(ipFamily, ipFamilySubnets)
			t.ensureNumGatewayRoutes(1)
		})

		Context("and the GatewayRoute operations initially fail", func() {
			BeforeEach(func() {
				r := fake.NewFailingReactorForResource(&t.submClient.Fake, "gatewayroutes")
				r.SetResetOnFailure(true)
				r.SetFailOnCreate(errors.New("mock GatewayRoute create error"))
				r.SetFailOnDelete(errors.New("mock GatewayRoute delete error"))
			})

			It("should eventually create/delete a GatewayRoute", func() {
				gwRouteName := t.awaitGatewayRoute(ipFamily, nil)

				t.DeleteEndpoint(endpoint.Name)
				test.AwaitNoResource(ovn.GatewayResourceInterface(t.submClient, testing.Namespace), gwRouteName)
			})
		})
	})
}

func (t *gwRouteHandlerTestDriver) awaitGatewayRoute(ipFamily k8snet.IPFamily, subnets []string) string {
	var gwRoute *submarinerv1.GatewayRoute

	Eventually(func(g Gomega) {
		list, err := t.submClient.SubmarinerV1().GatewayRoutes(testing.Namespace).List(context.TODO(), metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())

		for i := range list.Items {
			Expect(list.Items[i].RoutePolicySpec.NextHops).To(HaveLen(1))

			if k8snet.IPFamilyOfString(list.Items[i].RoutePolicySpec.NextHops[0]) != ipFamily {
				continue
			}

			gwRoute = &list.Items[i]

			if len(subnets) > 0 {
				g.Expect(gwRoute.RoutePolicySpec.RemoteCIDRs).To(Equal(subnets))
			}

			g.Expect(gwRoute.RoutePolicySpec.NextHops[0]).To(Equal(t.OVNK8sMgmntIntCIDR[ipFamily].IP.String()))
		}

		g.Expect(gwRoute).NotTo(BeNil(), "GatewayRoute for IPv%s not found", ipFamily)
	}).Within(time.Second * 3).Should(Succeed())

	return gwRoute.Name
}

func (t *gwRouteHandlerTestDriver) ensureNumGatewayRoutes(num int) {
	Consistently(func() int {
		list, err := t.submClient.SubmarinerV1().GatewayRoutes(testing.Namespace).List(context.TODO(), metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())

		return len(list.Items)
	}).Should(Equal(num))
}
