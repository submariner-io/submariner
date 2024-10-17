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
	"errors"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/admiral/pkg/test"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event/testing"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/ovn"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("NonGatewayRouteHandler", func() {
	t := newTestDriver()

	JustBeforeEach(func() {
		t.Start(ovn.NewNonGatewayRouteHandler(t.submClient, t.k8sClient, ovn.NewTransitSwitchIP()))
	})

	awaitNonGatewayRoute := func(ep *submarinerv1.Endpoint) {
		nonGWRoute := test.AwaitResource(ovn.NonGatewayResourceInterface(t.submClient, testing.Namespace), ep.Spec.ClusterID)
		Expect(nonGWRoute.RoutePolicySpec.RemoteCIDRs).To(Equal(ep.Spec.Subnets))
		Expect(nonGWRoute.RoutePolicySpec.NextHops).To(Equal([]string{t.transitSwitchIP}))
	}

	When("a remote Endpoint is created and deleted on the gateway", func() {
		JustBeforeEach(func() {
			t.CreateLocalHostEndpoint()
		})

		It("should create/delete a NonGatewayRoute", func() {
			endpoint := t.CreateEndpoint(testing.NewEndpoint("remote-cluster", "host", "193.0.4.0/24"))
			awaitNonGatewayRoute(endpoint)

			t.DeleteEndpoint(endpoint.Name)
			test.AwaitNoResource(ovn.NonGatewayResourceInterface(t.submClient, testing.Namespace), endpoint.Spec.ClusterID)
		})

		Context("and the NonGatewayRoute operations initially fail", func() {
			JustBeforeEach(func() {
				r := fake.NewFailingReactorForResource(&t.submClient.Fake, "nongatewayroutes")
				r.SetResetOnFailure(true)
				r.SetFailOnCreate(errors.New("mock NonGatewayRoute create error"))
				r.SetFailOnDelete(errors.New("mock NonGatewayRoute delete error"))
			})

			It("should eventually create/delete a NonGatewayRoute", func() {
				endpoint := t.CreateEndpoint(testing.NewEndpoint("remote-cluster", "host", "193.0.4.0/24"))
				awaitNonGatewayRoute(endpoint)

				t.DeleteEndpoint(endpoint.Name)
				test.AwaitNoResource(ovn.NonGatewayResourceInterface(t.submClient, testing.Namespace), endpoint.Spec.ClusterID)
			})
		})

		Context("and no transit switch IP configured", func() {
			BeforeEach(func() {
				t.transitSwitchIP = ""
			})

			It("should not create a NonGatewayRoute", func() {
				endpoint := t.CreateEndpoint(testing.NewEndpoint("remote-cluster", "host", "193.0.4.0/24"))
				test.EnsureNoResource(ovn.NonGatewayResourceInterface(t.submClient, testing.Namespace), endpoint.Spec.ClusterID)

				t.submClient.Fake.ClearActions()
				t.DeleteEndpoint(endpoint.Name)
				test.EnsureNoActionsForResource(&t.submClient.Fake, "nongatewayroutes", "delete")
			})
		})
	})

	Context("on transition to gateway", func() {
		It("should create NonGatewayRoutes for all remote Endpoints", func() {
			endpoint := t.CreateEndpoint(testing.NewEndpoint("remote-cluster", "host", "193.0.4.0/24"))
			test.EnsureNoResource(ovn.NonGatewayResourceInterface(t.submClient, testing.Namespace), endpoint.Spec.ClusterID)

			localEndpoint := t.CreateLocalHostEndpoint()
			awaitNonGatewayRoute(endpoint)

			t.DeleteEndpoint(localEndpoint.Name)

			t.submClient.Fake.ClearActions()
			t.CreateLocalHostEndpoint()
			test.EnsureNoActionsForResource(&t.submClient.Fake, "nongatewayroutes", "create")
		})

		Context("with no transit switch IP configured", func() {
			BeforeEach(func() {
				t.transitSwitchIP = ""
			})

			It("should not create any NonGatewayRoutes", func() {
				endpoint := t.CreateEndpoint(testing.NewEndpoint("remote-cluster", "host", "193.0.4.0/24"))
				t.CreateLocalHostEndpoint()
				test.EnsureNoResource(ovn.NonGatewayResourceInterface(t.submClient, testing.Namespace), endpoint.Spec.ClusterID)
			})
		})
	})

	When("the local node's transit switch IP is updated", func() {
		JustBeforeEach(func() {
			t.CreateLocalHostEndpoint()
		})

		It("should update existing NonGatewayRoutes", func() {
			endpoint := t.CreateEndpoint(testing.NewEndpoint("remote-cluster", "host", "193.0.4.0/24"))
			awaitNonGatewayRoute(endpoint)

			t.transitSwitchIP = "10.34.87.2"

			t.UpdateNode(&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:        os.Getenv("NODE_NAME"),
					Annotations: map[string]string{constants.OvnTransitSwitchIPAnnotation: toTransitSwitchIPAnnotation(t.transitSwitchIP)},
				},
			})

			Eventually(func() string {
				return test.AwaitResource(ovn.NonGatewayResourceInterface(t.submClient, testing.Namespace),
					endpoint.Spec.ClusterID).RoutePolicySpec.NextHops[0]
			}).Should(Equal(t.transitSwitchIP))
		})
	})
})
