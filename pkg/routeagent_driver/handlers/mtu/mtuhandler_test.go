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
package mtu

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/iptables"
	fakeIPT "github.com/submariner-io/submariner/pkg/iptables/fake"
)

var _ = Describe("MTUHandler", func() {
	var (
		ipt     *fakeIPT.IPTables
		handler event.Handler
	)

	BeforeEach(func() {

		ipt = fakeIPT.New()
		iptables.NewFunc = func() (iptables.Interface, error) {
			return ipt, nil
		}
		handler = NewMTUHandler()
	})

	AfterEach(func() {
		iptables.NewFunc = nil

	})

	When("When endpoint is added and removed", func() {
		It("Should add and remove iptable rules", func() {
			Expect(handler.Init()).To(Succeed())
			submEndpoint := newSubmEndpoint()
			Expect(handler.RemoteEndpointCreated(submEndpoint)).To(Succeed())
			for _, subnet := range submEndpoint.Spec.Subnets {
				ipt.AwaitRule(mangleTable, subMarinerPostRoutingChain, ContainSubstring("-s "+subnet))
				ipt.AwaitRule(mangleTable, subMarinerPostRoutingChain, ContainSubstring("-d "+subnet))
			}
			Expect(handler.RemoteEndpointRemoved(submEndpoint)).To(Succeed())
			for _, subnet := range submEndpoint.Spec.Subnets {
				ipt.AwaitNoRule(mangleTable, subMarinerPostRoutingChain, ContainSubstring("-s "+subnet))
				ipt.AwaitNoRule(mangleTable, subMarinerPostRoutingChain, ContainSubstring("-d "+subnet))
			}
		})
	})
})

func newSubmEndpoint() *submV1.Endpoint {
	return &submV1.Endpoint{
		Spec: submV1.EndpointSpec{
			Subnets: []string{"10.0.0.0/24", "172.0.0.0/24"},
		},
	}
}
