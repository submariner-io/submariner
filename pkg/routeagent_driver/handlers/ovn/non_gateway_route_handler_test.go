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
	"net"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/admiral/pkg/test"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/event/testing"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/ovn"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8snet "k8s.io/utils/net"
)

var _ = Describe("NonGatewayRouteHandler", func() {
	ipv4Subnets := []string{"193.0.4.0/24", "194.0.4.0/24"}
	ipv6Subnets := []string{"ec00:abcd::/64", "ed00:abcd::/64"}

	t := &nonGWRouteHandlerTestDriver{testDriver: newTestDriver()}

	Context("IPv4", func() {
		t.testRemoteEndpoints(k8snet.IPv4, ipv4Subnets, ipv6Subnets)
	})

	Context("IPv6", func() {
		t.testRemoteEndpoints(k8snet.IPv6, ipv6Subnets, ipv4Subnets)
	})

	Context("Dual-stack", func() {
		JustBeforeEach(func(ctx context.Context) {
			t.start(ctx, k8snet.IPv4, k8snet.IPv6)
			t.CreateLocalHostEndpoint(ctx)
		})

		It("should create NonGatewayRoutes for IPv4 and IPv6", func(ctx context.Context) {
			t.createEndpoint(ctx, append(ipv6Subnets, ipv4Subnets...)...)

			t.awaitNonGatewayRoute(ctx, k8snet.IPv4, ipv4Subnets)
			t.awaitNonGatewayRoute(ctx, k8snet.IPv6, ipv6Subnets)
		})
	})

	Context("on transition to gateway", func() {
		JustBeforeEach(func(ctx context.Context) {
			t.start(ctx, k8snet.IPv4)
		})

		It("should create NonGatewayRoutes for all remote Endpoints", func(ctx context.Context) {
			t.createEndpoint(ctx, ipv4Subnets...)
			t.ensureNumNonGatewayRoutes(ctx, 0)

			localEndpoint := t.CreateLocalHostEndpoint(ctx)
			t.awaitNonGatewayRoute(ctx, k8snet.IPv4, ipv4Subnets)

			t.DeleteEndpoint(ctx, localEndpoint.Name)

			t.submClient.Fake.ClearActions()
			t.CreateLocalHostEndpoint(ctx)

			test.EnsureNoActionsForResource(&t.submClient.Fake, "nongatewayroutes", "create")
		})

		Context("with no transit switch IP configured", func() {
			BeforeEach(func() {
				t.transitSwitchIP = map[k8snet.IPFamily]string{}
			})

			It("should not create any NonGatewayRoutes", func(ctx context.Context) {
				t.createEndpoint(ctx, ipv4Subnets...)
				t.CreateLocalHostEndpoint(ctx)
				t.ensureNumNonGatewayRoutes(ctx, 0)
			})
		})
	})
})

type nonGWRouteHandlerTestDriver struct {
	*testDriver
}

func (t *nonGWRouteHandlerTestDriver) start(ctx context.Context, ipFamilies ...k8snet.IPFamily) {
	h := make([]event.Handler, len(ipFamilies))

	for i := range ipFamilies {
		tsIP := ovn.NewTransitSwitchIP(ipFamilies[i])
		Expect(tsIP.Init(ctx, t.k8sClient)).To(Succeed())
		h[i] = ovn.NewNonGatewayRouteHandler(ipFamilies[i], t.submClient, tsIP)
	}

	t.Start(ctx, h...)
	t.CreateNode(ctx, t.node)
}

func (t *nonGWRouteHandlerTestDriver) testRemoteEndpoints(ipFamily k8snet.IPFamily, ipFamilySubnets, nonIPFamilySubnets []string) {
	var endpoint *submarinerv1.Endpoint

	JustBeforeEach(func(ctx context.Context) {
		t.start(ctx, ipFamily)

		t.CreateLocalHostEndpoint(ctx)

		By(fmt.Sprintf("Creating remote Endpoint with subnets %v", ipFamilySubnets))

		endpoint = t.createEndpoint(ctx, ipFamilySubnets...)
	})

	When("a remote Endpoint is created and deleted on the gateway", func() {
		It("should create/delete NonGatewayRoutes", func(ctx context.Context) {
			nonGWRouteName := t.awaitNonGatewayRoute(ctx, ipFamily, ipFamilySubnets)

			t.CreateEndpoint(ctx, testing.NewEndpoint("other"+remoteClusterID, "host", nonIPFamilySubnets...))
			t.ensureNumNonGatewayRoutes(ctx, 1)

			By("Deleting remote Endpoint")

			t.DeleteEndpoint(ctx, endpoint.Name)
			test.AwaitNoResource(ctx, ovn.NonGatewayResourceInterface(t.submClient, testing.Namespace), nonGWRouteName)

			By(fmt.Sprintf("Creating remote Endpoint with subnets %v", append(ipFamilySubnets, nonIPFamilySubnets...)))

			t.createEndpoint(ctx, append(ipFamilySubnets, nonIPFamilySubnets...)...)
			t.awaitNonGatewayRoute(ctx, ipFamily, ipFamilySubnets)
			t.ensureNumNonGatewayRoutes(ctx, 1)
		})

		Context("and the NonGatewayRoute operations initially fail", func() {
			JustBeforeEach(func() {
				r := fake.NewFailingReactorForResource(&t.submClient.Fake, "nongatewayroutes")
				r.SetResetOnFailure(true)
				r.SetFailOnCreate(errors.New("mock NonGatewayRoute create error"))
				r.SetFailOnDelete(errors.New("mock NonGatewayRoute delete error"))
			})

			It("should eventually create/delete a NonGatewayRoute", func(ctx context.Context) {
				nonGWRouteName := t.awaitNonGatewayRoute(ctx, ipFamily, nil)

				t.DeleteEndpoint(ctx, endpoint.Name)
				test.AwaitNoResource(ctx, ovn.NonGatewayResourceInterface(t.submClient, testing.Namespace), nonGWRouteName)
			})
		})

		Context("and no transit switch IP configured", func() {
			BeforeEach(func() {
				t.transitSwitchIP = map[k8snet.IPFamily]string{}
			})

			It("should not create a NonGatewayRoute", func(ctx context.Context) {
				t.ensureNumNonGatewayRoutes(ctx, 0)

				t.submClient.Fake.ClearActions()
				t.DeleteEndpoint(ctx, endpoint.Name)
				test.EnsureNoActionsForResource(&t.submClient.Fake, "nongatewayroutes", "delete")
			})
		})
	})

	When("the local node's transit switch IP is updated", func() {
		It("should update existing NonGatewayRoutes", func(ctx context.Context) {
			t.awaitNonGatewayRoute(ctx, ipFamily, ipFamilySubnets)

			newIP := net.ParseIP(t.transitSwitchIP[ipFamily])
			newIP[len(newIP)-1]++
			t.transitSwitchIP[ipFamily] = newIP.String()

			t.UpdateNode(ctx, &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: os.Getenv("NODE_NAME"),
					Annotations: map[string]string{
						constants.OvnTransitSwitchIPAnnotation: toTransitSwitchIPAnnotation(t.transitSwitchIP[k8snet.IPv4], t.transitSwitchIP[k8snet.IPv6]),
					},
				},
			})

			t.awaitNonGatewayRoute(ctx, ipFamily, ipFamilySubnets)
		})
	})
}

func (t *nonGWRouteHandlerTestDriver) awaitNonGatewayRoute(ctx context.Context, ipFamily k8snet.IPFamily, subnets []string) string {
	var nonGWRoute *submarinerv1.NonGatewayRoute

	Eventually(func(g Gomega) {
		list, err := t.submClient.SubmarinerV1().NonGatewayRoutes(testing.Namespace).List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())

		for i := range list.Items {
			Expect(list.Items[i].RoutePolicySpec.NextHops).To(HaveLen(1))

			if k8snet.IPFamilyOfString(list.Items[i].RoutePolicySpec.NextHops[0]) != ipFamily {
				continue
			}

			nonGWRoute = &list.Items[i]

			if len(subnets) > 0 {
				g.Expect(nonGWRoute.RoutePolicySpec.RemoteCIDRs).To(Equal(subnets))
			}

			g.Expect(nonGWRoute.RoutePolicySpec.NextHops[0]).To(Equal(t.transitSwitchIP[ipFamily]))
		}

		g.Expect(nonGWRoute).NotTo(BeNil(), "NonGatewayRoute for IPv%s not found", ipFamily)
	}).Within(time.Second * 3).Should(Succeed())

	return nonGWRoute.Name
}

func (t *nonGWRouteHandlerTestDriver) ensureNumNonGatewayRoutes(ctx context.Context, num int) {
	Consistently(func() int {
		list, err := t.submClient.SubmarinerV1().NonGatewayRoutes(testing.Namespace).List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())

		return len(list.Items)
	}).Should(Equal(num))
}
