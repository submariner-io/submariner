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
package mtu_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	fakePF "github.com/submariner-io/submariner/pkg/packetfilter/fake"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/mtu"
)

var _ = Describe("MTUHandler", func() {
	var (
		pFilter *fakePF.PacketFilter
		handler event.Handler
	)

	BeforeEach(func() {
		pFilter = fakePF.New()
		handler = mtu.NewMTUHandler([]string{"10.1.0.0/24"}, false, 0)
	})

	When("endpoint is added and removed", func() {
		It("should add and remove iptable rules", func() {
			Expect(handler.Init()).To(Succeed())
			pFilter.AwaitRule(packetfilter.TableTypeRoute,
				constants.SmPostRoutingChain, ContainSubstring(fmt.Sprintf("\"SrcSetName\":%q", constants.RemoteCIDRIPSet)))
			pFilter.AwaitRule(packetfilter.TableTypeRoute,
				constants.SmPostRoutingChain, ContainSubstring(fmt.Sprintf("\"DestSetName\":%q", constants.RemoteCIDRIPSet)))
			pFilter.AwaitRule(packetfilter.TableTypeRoute,
				constants.SmPostRoutingChain, ContainSubstring(fmt.Sprintf("\"SrcSetName\":%q", constants.LocalCIDRIPSet)))
			pFilter.AwaitRule(packetfilter.TableTypeRoute,
				constants.SmPostRoutingChain, ContainSubstring(fmt.Sprintf("\"DestSetName\":%q", constants.LocalCIDRIPSet)))

			localEndpoint := newSubmEndpoint([]string{"10.1.0.0/24", "172.1.0.0/24"})
			Expect(handler.LocalEndpointCreated(localEndpoint)).To(Succeed())
			for _, subnet := range localEndpoint.Spec.Subnets {
				pFilter.AwaitEntry(constants.LocalCIDRIPSet, subnet)
			}
			Expect(handler.LocalEndpointRemoved(localEndpoint)).To(Succeed())
			for _, subnet := range localEndpoint.Spec.Subnets {
				pFilter.AwaitNoEntry(constants.LocalCIDRIPSet, subnet)
			}

			remoteEndpoint := newSubmEndpoint([]string{"10.0.0.0/24", "172.0.0.0/24"})
			Expect(handler.RemoteEndpointCreated(remoteEndpoint)).To(Succeed())
			for _, subnet := range remoteEndpoint.Spec.Subnets {
				pFilter.AwaitEntry(constants.RemoteCIDRIPSet, subnet)
			}
			Expect(handler.RemoteEndpointRemoved(remoteEndpoint)).To(Succeed())
			for _, subnet := range remoteEndpoint.Spec.Subnets {
				pFilter.AwaitNoEntry(constants.RemoteCIDRIPSet, subnet)
			}
		})
	})
})

func newSubmEndpoint(subnets []string) *submV1.Endpoint {
	return &submV1.Endpoint{
		Spec: submV1.EndpointSpec{
			Subnets: subnets,
		},
	}
}
