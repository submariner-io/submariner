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
	"context"
	"net"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	fakenetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	fakePF "github.com/submariner-io/submariner/pkg/packetfilter/fake"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/mtu"
	k8snet "k8s.io/utils/net"
)

const localCIDR = "10.1.0.0/24"

var _ = Describe("MTUHandler", func() {
	t := newTestDriver()

	Specify("Init should add expected IP sets and table rules", func() {
		t.pFilter.AwaitSet(Equal(constants.LocalCIDRIPSet))
		t.pFilter.AwaitSet(Equal(constants.RemoteCIDRIPSet))

		t.pFilter.AwaitRule(t.tableType,
			constants.SmPostRoutingMssChain, And(
				ContainSubstring("\"ClampType\":%d", packetfilter.ToPMTU),
				ContainSubstring("\"SrcSetName\":%q", constants.RemoteCIDRIPSet),
				ContainSubstring("\"DestSetName\":%q", constants.LocalCIDRIPSet)))
		t.pFilter.AwaitRule(t.tableType,
			constants.SmPostRoutingMssChain, And(
				ContainSubstring("\"ClampType\":%d", packetfilter.ToPMTU),
				ContainSubstring("\"SrcSetName\":%q", constants.LocalCIDRIPSet),
				ContainSubstring("\"DestSetName\":%q", constants.RemoteCIDRIPSet)))
	})

	When("a local Endpoint is added and removed", func() {
		It("should add and remove IP set entries", func() {
			localEndpoint := newSubmEndpoint([]string{"172.1.0.0/24", "172.2.0.0/24"})
			Expect(t.handler.LocalEndpointCreated(localEndpoint)).To(Succeed())

			for _, subnet := range localEndpoint.Spec.Subnets {
				t.pFilter.AwaitEntry(constants.LocalCIDRIPSet, subnet)
			}

			t.pFilter.AwaitEntry(constants.LocalCIDRIPSet, localCIDR)

			Expect(t.handler.LocalEndpointRemoved(localEndpoint)).To(Succeed())

			for _, subnet := range localEndpoint.Spec.Subnets {
				t.pFilter.AwaitNoEntry(constants.LocalCIDRIPSet, subnet)
			}

			t.pFilter.AwaitNoEntry(constants.LocalCIDRIPSet, localCIDR)
		})
	})

	When("a remote Endpoint is added and removed", func() {
		It("should add and remove IP set entries", func() {
			remoteEndpoint := newSubmEndpoint([]string{"10.0.0.0/24", "172.0.0.0/24"})
			Expect(t.handler.RemoteEndpointCreated(remoteEndpoint)).To(Succeed())

			for _, subnet := range remoteEndpoint.Spec.Subnets {
				t.pFilter.AwaitEntry(constants.RemoteCIDRIPSet, subnet)
			}

			Expect(t.handler.RemoteEndpointRemoved(remoteEndpoint)).To(Succeed())

			for _, subnet := range remoteEndpoint.Spec.Subnets {
				t.pFilter.AwaitNoEntry(constants.RemoteCIDRIPSet, subnet)
			}
		})
	})

	When("TCP MSS is forced to a specific value and a local Endpoint is created", func() {
		BeforeEach(func() {
			t.tcpMssValue = 10
		})

		It("should add expected IP table rules", func() {
			t.testForcedMSS(t.tcpMssValue)
		})
	})

	When("Globalnet is enabled with no TCP MSS value specified and a local Endpoint is created", func() {
		const defaultMTU = 1965

		BeforeEach(func() {
			t.isGlobalnet = true
			t.netLink.SetupDefaultGateway(k8snet.IPv4, net.Interface{MTU: defaultMTU})
		})

		It("should use the MTU value from the default gateway and add expected IP table rules", func() {
			t.testForcedMSS(defaultMTU - mtu.MaxIPSecOverhead)
		})
	})

	Specify("Uninstall should remove IP sets and chains", func() {
		Expect(t.handler.Uninstall()).To(Succeed())

		t.pFilter.AwaitSetDeleted(constants.LocalCIDRIPSet)
		t.pFilter.AwaitSetDeleted(constants.RemoteCIDRIPSet)
		t.pFilter.AwaitNoIPHookChain(packetfilter.ChainTypeRoute, Equal(constants.SmPostRoutingMssChain))
	})
})

type testDriver struct {
	pFilter     *fakePF.PacketFilter
	netLink     *fakenetlink.NetLink
	handler     event.Handler
	tcpMssValue int
	isGlobalnet bool
	tableType   packetfilter.TableType
}

func newTestDriver() *testDriver {
	t := &testDriver{}

	BeforeEach(func() {
		t.tcpMssValue = 0
		t.isGlobalnet = false
		t.pFilter = fakePF.New()
		t.tableType, _ = t.pFilter.GetMSSClampTypes()

		t.netLink = fakenetlink.New()
		netlinkAPI.NewFunc = func() netlinkAPI.Interface {
			return t.netLink
		}
	})

	JustBeforeEach(func() {
		t.handler = mtu.NewMTUHandler([]string{localCIDR}, t.isGlobalnet, t.tcpMssValue)
		Expect(t.handler.Init(context.TODO())).To(Succeed())
	})

	return t
}

func (t *testDriver) testForcedMSS(expTCPMssValue int) {
	t.pFilter.AwaitSet(Equal(constants.LocalCIDRIPSet))
	t.pFilter.AwaitSet(Equal(constants.RemoteCIDRIPSet))
	t.pFilter.EnsureNoRule(t.tableType, constants.SmPostRoutingMssChain,
		ContainSubstring("\"ClampType\":%d", packetfilter.ToPMTU))

	Expect(t.handler.LocalEndpointCreated(newSubmEndpoint([]string{"172.1.0.0/24"}))).To(Succeed())

	t.pFilter.AwaitRule(t.tableType,
		constants.SmPostRoutingMssChain, And(
			ContainSubstring("\"ClampType\":%d", packetfilter.ToValue),
			ContainSubstring("\"SrcSetName\":%q", constants.RemoteCIDRIPSet),
			ContainSubstring("\"DestSetName\":%q", constants.LocalCIDRIPSet),
			ContainSubstring("\"MssValue\":%q", strconv.Itoa(expTCPMssValue))))
	t.pFilter.AwaitRule(t.tableType,
		constants.SmPostRoutingMssChain, And(
			ContainSubstring("\"ClampType\":%d", packetfilter.ToValue),
			ContainSubstring("\"SrcSetName\":%q", constants.LocalCIDRIPSet),
			ContainSubstring("\"DestSetName\":%q", constants.RemoteCIDRIPSet),
			ContainSubstring("\"MssValue\":%q", strconv.Itoa(expTCPMssValue))))
}

func newSubmEndpoint(subnets []string) *submV1.Endpoint {
	return &submV1.Endpoint{
		Spec: submV1.EndpointSpec{
			Subnets: subnets,
		},
	}
}
