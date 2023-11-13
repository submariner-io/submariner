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
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/admiral/pkg/test"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event/testing"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/ovn"
	"github.com/vishvananda/netlink"
)

var _ = Describe("GatewayRouteHandler", func() {
	t := newTestDriver()

	var nextHopIP *net.IPNet

	BeforeEach(func() {
		nextHopIP = &net.IPNet{
			IP: []byte{128, 1, 20, 2},
		}
	})

	JustBeforeEach(func() {
		link := &netlink.GenericLink{
			LinkAttrs: netlink.LinkAttrs{
				Index: 99,
				Name:  ovn.OVNK8sMgmntIntfName,
			},
		}

		t.netLink.SetLinkIndex(ovn.OVNK8sMgmntIntfName, link.Index)
		Expect(t.netLink.LinkAdd(link)).To(Succeed())
		Expect(t.netLink.AddrAdd(link, &netlink.Addr{
			IPNet: nextHopIP,
		})).To(Succeed())

		t.Start(ovn.NewGatewayRouteHandler(t.submClient))
	})

	awaitGatewayRoute := func(ep *submarinerv1.Endpoint) {
		gwRoute := test.AwaitResource(ovn.GatewayResourceInterface(t.submClient, testing.Namespace), ep.Name)
		Expect(gwRoute.RoutePolicySpec.RemoteCIDRs).To(Equal(ep.Spec.Subnets))
		Expect(gwRoute.RoutePolicySpec.NextHops).To(Equal([]string{nextHopIP.IP.String()}))
	}

	When("a remote Endpoint is created and deleted on the gateway", func() {
		JustBeforeEach(func() {
			t.CreateLocalHostEndpoint()
		})

		It("should create/delete a GatewayRoute", func() {
			endpoint := t.CreateEndpoint(testing.NewEndpoint("remote-cluster1", "host", "192.0.4.0/24"))
			awaitGatewayRoute(endpoint)

			t.DeleteEndpoint(endpoint.Name)
			test.AwaitNoResource(ovn.GatewayResourceInterface(t.submClient, testing.Namespace), endpoint.Name)
		})

		Context("and the GatewayRoute operations initially fail", func() {
			JustBeforeEach(func() {
				r := fake.NewFailingReactorForResource(&t.submClient.Fake, "gatewayroutes")
				r.SetResetOnFailure(true)
				r.SetFailOnCreate(errors.New("mock GatewayRoute create error"))
				r.SetFailOnDelete(errors.New("mock GatewayRoute delete error"))
			})

			It("should eventually create/delete a GatewayRoute", func() {
				endpoint := t.CreateEndpoint(testing.NewEndpoint("remote-cluster1", "host", "192.0.4.0/24"))
				awaitGatewayRoute(endpoint)

				t.DeleteEndpoint(endpoint.Name)
				test.AwaitNoResource(ovn.GatewayResourceInterface(t.submClient, testing.Namespace), endpoint.Name)
			})
		})
	})

	Context("on transition to gateway", func() {
		It("should create GatewayRoutes for all remote Endpoints", func() {
			endpoint := t.CreateEndpoint(testing.NewEndpoint("remote-cluster1", "host", "192.0.4.0/24"))
			test.EnsureNoResource(ovn.GatewayResourceInterface(t.submClient, testing.Namespace), endpoint.Name)

			localEndpoint := t.CreateLocalHostEndpoint()
			awaitGatewayRoute(endpoint)

			t.DeleteEndpoint(localEndpoint.Name)

			t.submClient.Fake.ClearActions()
			t.CreateLocalHostEndpoint()
			test.EnsureNoActionsForResource(&t.submClient.Fake, "gatewayroutes", "create")
		})
	})
})
