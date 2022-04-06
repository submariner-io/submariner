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
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/ipset"
	fakeSet "github.com/submariner-io/submariner/pkg/ipset/fake"
	"github.com/submariner-io/submariner/pkg/iptables"
	fakeIPT "github.com/submariner-io/submariner/pkg/iptables/fake"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/mtu"
)

var _ = Describe("MTUHandler", func() {
	var (
		ipt     *fakeIPT.IPTables
		ipSet   *fakeSet.IPSet
		handler event.Handler
	)

	BeforeEach(func() {
		ipt = fakeIPT.New()
		iptables.NewFunc = func() (iptables.Interface, error) {
			return ipt, nil
		}
		ipSet = fakeSet.New()
		ipset.NewFunc = func() ipset.Interface {
			return ipSet
		}
		handler = mtu.NewMTUHandler()
	})

	AfterEach(func() {
		iptables.NewFunc = nil
	})

	When("endpoint is added and removed", func() {
		It("should add and remove iptable rules", func() {
			Expect(handler.Init()).To(Succeed())
			ipt.AwaitRule(constants.MangleTable, constants.SmPostRoutingChain, ContainSubstring(constants.RemoteCIDRIPSet+" src"))
			ipt.AwaitRule(constants.MangleTable, constants.SmPostRoutingChain, ContainSubstring(constants.RemoteCIDRIPSet+" dst"))
			ipt.AwaitRule(constants.MangleTable, constants.SmPostRoutingChain, ContainSubstring(constants.LocalCIDRIPSet+" src"))
			ipt.AwaitRule(constants.MangleTable, constants.SmPostRoutingChain, ContainSubstring(constants.LocalCIDRIPSet+" dst"))

			localEndpoint := newSubmEndpoint([]string{"10.1.0.0/24", "172.1.0.0/24"})
			Expect(handler.LocalEndpointCreated(localEndpoint)).To(Succeed())
			for _, subnet := range localEndpoint.Spec.Subnets {
				ipSet.AwaitEntry(constants.LocalCIDRIPSet, subnet)
			}
			Expect(handler.LocalEndpointRemoved(localEndpoint)).To(Succeed())
			for _, subnet := range localEndpoint.Spec.Subnets {
				ipSet.AwaitNoEntry(constants.LocalCIDRIPSet, subnet)
			}

			remoteEndpoint := newSubmEndpoint([]string{"10.0.0.0/24", "172.0.0.0/24"})
			Expect(handler.RemoteEndpointCreated(remoteEndpoint)).To(Succeed())
			for _, subnet := range remoteEndpoint.Spec.Subnets {
				ipSet.AwaitEntry(constants.RemoteCIDRIPSet, subnet)
			}
			Expect(handler.RemoteEndpointRemoved(remoteEndpoint)).To(Succeed())
			for _, subnet := range remoteEndpoint.Spec.Subnets {
				ipSet.AwaitNoEntry(constants.RemoteCIDRIPSet, subnet)
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
