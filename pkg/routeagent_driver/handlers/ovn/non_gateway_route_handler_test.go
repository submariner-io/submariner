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
		JustBeforeEach(func() {
			t.start(k8snet.IPv4, k8snet.IPv6)
			t.CreateLocalHostEndpoint()
		})

		It("should create NonGatewayRoutes for IPv4 and IPv6", func() {
			t.createEndpoint(append(ipv6Subnets, ipv4Subnets...)...)

			t.awaitNonGatewayRoute(k8snet.IPv4, ipv4Subnets)
			t.awaitNonGatewayRoute(k8snet.IPv6, ipv6Subnets)
		})
	})

	Context("on transition to gateway", func() {
		JustBeforeEach(func() {
			t.start(k8snet.IPv4)
		})

		It("should create NonGatewayRoutes for all remote Endpoints", func() {
			t.createEndpoint(ipv4Subnets...)
			t.ensureNumNonGatewayRoutes(0)

			localEndpoint := t.CreateLocalHostEndpoint()
			t.awaitNonGatewayRoute(k8snet.IPv4, ipv4Subnets)

			t.DeleteEndpoint(localEndpoint.Name)

			t.submClient.Fake.ClearActions()
			t.CreateLocalHostEndpoint()

			test.EnsureNoActionsForResource(&t.submClient.Fake, "nongatewayroutes", "create")
		})

		Context("with no transit switch IP configured", func() {
			BeforeEach(func() {
				t.transitSwitchIP = map[k8snet.IPFamily]string{}
			})

			It("should not create any NonGatewayRoutes", func() {
				t.createEndpoint(ipv4Subnets...)
				t.CreateLocalHostEndpoint()
				t.ensureNumNonGatewayRoutes(0)
			})
		})
	})
})

type nonGWRouteHandlerTestDriver struct {
	*testDriver
}

func (t *nonGWRouteHandlerTestDriver) start(ipFamilies ...k8snet.IPFamily) {
	h := make([]event.Handler, len(ipFamilies))

	for i := range ipFamilies {
		tsIP := ovn.NewTransitSwitchIP(ipFamilies[i])
		Expect(tsIP.Init(context.TODO(), t.k8sClient)).To(Succeed())
		h[i] = ovn.NewNonGatewayRouteHandler(ipFamilies[i], t.submClient, tsIP)
	}

	t.Start(h...)
	t.CreateNode(t.node)
}

func (t *nonGWRouteHandlerTestDriver) testRemoteEndpoints(ipFamily k8snet.IPFamily, ipFamilySubnets, nonIPFamilySubnets []string) {
	var endpoint *submarinerv1.Endpoint

	JustBeforeEach(func() {
		t.start(ipFamily)

		t.CreateLocalHostEndpoint()

		By(fmt.Sprintf("Creating remote Endpoint with subnets %v", ipFamilySubnets))

		endpoint = t.createEndpoint(ipFamilySubnets...)
	})

	When("a remote Endpoint is created and deleted on the gateway", func() {
		It("should create/delete NonGatewayRoutes", func() {
			nonGWRouteName := t.awaitNonGatewayRoute(ipFamily, ipFamilySubnets)

			t.CreateEndpoint(testing.NewEndpoint("other"+remoteClusterID, "host", nonIPFamilySubnets...))
			t.ensureNumNonGatewayRoutes(1)

			By("Deleting remote Endpoint")

			t.DeleteEndpoint(endpoint.Name)
			test.AwaitNoResource(ovn.NonGatewayResourceInterface(t.submClient, testing.Namespace), nonGWRouteName)

			By(fmt.Sprintf("Creating remote Endpoint with subnets %v", append(ipFamilySubnets, nonIPFamilySubnets...)))

			t.createEndpoint(append(ipFamilySubnets, nonIPFamilySubnets...)...)
			t.awaitNonGatewayRoute(ipFamily, ipFamilySubnets)
			t.ensureNumNonGatewayRoutes(1)
		})

		Context("and the NonGatewayRoute operations initially fail", func() {
			JustBeforeEach(func() {
				r := fake.NewFailingReactorForResource(&t.submClient.Fake, "nongatewayroutes")
				r.SetResetOnFailure(true)
				r.SetFailOnCreate(errors.New("mock NonGatewayRoute create error"))
				r.SetFailOnDelete(errors.New("mock NonGatewayRoute delete error"))
			})

			It("should eventually create/delete a NonGatewayRoute", func() {
				nonGWRouteName := t.awaitNonGatewayRoute(ipFamily, nil)

				t.DeleteEndpoint(endpoint.Name)
				test.AwaitNoResource(ovn.NonGatewayResourceInterface(t.submClient, testing.Namespace), nonGWRouteName)
			})
		})

		Context("and no transit switch IP configured", func() {
			BeforeEach(func() {
				t.transitSwitchIP = map[k8snet.IPFamily]string{}
			})

			It("should not create a NonGatewayRoute", func() {
				t.ensureNumNonGatewayRoutes(0)

				t.submClient.Fake.ClearActions()
				t.DeleteEndpoint(endpoint.Name)
				test.EnsureNoActionsForResource(&t.submClient.Fake, "nongatewayroutes", "delete")
			})
		})
	})

	When("the local node's transit switch IP is updated", func() {
		It("should update existing NonGatewayRoutes", func() {
			t.awaitNonGatewayRoute(ipFamily, ipFamilySubnets)

			newIP := net.ParseIP(t.transitSwitchIP[ipFamily])
			newIP[len(newIP)-1]++
			t.transitSwitchIP[ipFamily] = newIP.String()

			t.UpdateNode(&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: os.Getenv("NODE_NAME"),
					Annotations: map[string]string{
						constants.OvnTransitSwitchIPAnnotation: toTransitSwitchIPAnnotation(t.transitSwitchIP[k8snet.IPv4], t.transitSwitchIP[k8snet.IPv6]),
					},
				},
			})

			t.awaitNonGatewayRoute(ipFamily, ipFamilySubnets)
		})
	})
}

func (t *nonGWRouteHandlerTestDriver) awaitNonGatewayRoute(ipFamily k8snet.IPFamily, subnets []string) string {
	var nonGWRoute *submarinerv1.NonGatewayRoute

	Eventually(func(g Gomega) {
		list, err := t.submClient.SubmarinerV1().NonGatewayRoutes(testing.Namespace).List(context.TODO(), metav1.ListOptions{})
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

func (t *nonGWRouteHandlerTestDriver) ensureNumNonGatewayRoutes(num int) {
	Consistently(func() int {
		list, err := t.submClient.SubmarinerV1().NonGatewayRoutes(testing.Namespace).List(context.TODO(), metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())

		return len(list.Items)
	}).Should(Equal(num))
}
