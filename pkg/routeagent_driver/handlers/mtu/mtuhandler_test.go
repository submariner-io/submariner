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

const (
	localIPv4CIDR     = "10.1.0.0/24"
	localIPv6CIDR     = "2001:0:0:1234::/64"
	localIPv4Subnet1  = "172.1.0.0/24"
	localIPv4Subnet2  = "172.2.0.0/24"
	localIPv6Subnet1  = "3001:0:0:1234::/64"
	localIPv6Subnet2  = "3002:0:0:1234::/64"
	remoteIPv4Subnet1 = "10.0.0.0/24"
	remoteIPv4Subnet2 = "10.1.0.0/24"
	remoteIPv6Subnet1 = "4001:0:0:1234::/64"
	remoteIPv6Subnet2 = "4001:0:0:1234::/64"
)

var _ = Describe("MTU Handler", func() {
	t := newTestDriver()

	Context("IPv4", func() {
		t.testHandler(localIPv4CIDR, mtu.LocalCIDRIPSetIPv4, mtu.RemoteCIDRIPSetIPv4, []string{localIPv4Subnet1, localIPv4Subnet2},
			[]string{remoteIPv4Subnet1, remoteIPv4Subnet2})
	})

	Context("IPv6", func() {
		BeforeEach(func() {
			t.ipFamily = k8snet.IPv6
		})

		t.testHandler(localIPv6CIDR, mtu.LocalCIDRIPSetIPv6, mtu.RemoteCIDRIPSetIPv6, []string{localIPv6Subnet1, localIPv6Subnet2},
			[]string{remoteIPv6Subnet1, remoteIPv6Subnet2})
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
			defaultHostIface, err := t.netLink.GetDefaultGatewayInterface(t.ipFamily)
			Expect(err).To(Succeed())

			t.testForcedMSS(defaultHostIface.MTU() - mtu.MaxIPSecOverhead)
		})
	})

	Specify("GetNetworkPlugins should return any", func() {
		Expect(t.handler.GetNetworkPlugins()).To(ContainElement(event.AnyNetworkPlugin))
	})
})

type testDriver struct {
	ipFamily    k8snet.IPFamily
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
		t.ipFamily = k8snet.IPv4
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
		t.pFilter = fakePF.New(t.ipFamily)
		t.tableType, _ = t.pFilter.GetMSSClampTypes()

		t.handler = mtu.NewHandler(t.ipFamily, []string{localIPv4CIDR, localIPv6CIDR}, t.isGlobalnet, t.tcpMssValue)
		Expect(t.handler.Init(context.TODO())).To(Succeed())
	})

	return t
}

func (t *testDriver) testForcedMSS(expTCPMssValue int) {
	t.pFilter.AwaitSet(Equal(mtu.LocalCIDRIPSetIPv4))
	t.pFilter.AwaitSet(Equal(mtu.RemoteCIDRIPSetIPv4))
	t.pFilter.EnsureNoRule(t.tableType, constants.SmPostRoutingMssChain,
		ContainSubstring("\"ClampType\":%d", packetfilter.ToPMTU))

	Expect(t.handler.LocalEndpointCreated(newSubmEndpoint([]string{localIPv4Subnet1}))).To(Succeed())

	t.pFilter.AwaitRule(t.tableType,
		constants.SmPostRoutingMssChain, And(
			ContainSubstring("\"ClampType\":%d", packetfilter.ToValue),
			ContainSubstring("\"SrcSetName\":%q", mtu.RemoteCIDRIPSetIPv4),
			ContainSubstring("\"DestSetName\":%q", mtu.LocalCIDRIPSetIPv4),
			ContainSubstring("\"MssValue\":%q", strconv.Itoa(expTCPMssValue))))
	t.pFilter.AwaitRule(t.tableType,
		constants.SmPostRoutingMssChain, And(
			ContainSubstring("\"ClampType\":%d", packetfilter.ToValue),
			ContainSubstring("\"SrcSetName\":%q", mtu.LocalCIDRIPSetIPv4),
			ContainSubstring("\"DestSetName\":%q", mtu.RemoteCIDRIPSetIPv4),
			ContainSubstring("\"MssValue\":%q", strconv.Itoa(expTCPMssValue))))
}

func (t *testDriver) testHandler(localCIDR, localCIDRIPSet, remoteCIDRIPSet string, localSubnets, remoteSubnets []string) {
	Specify("Init should add expected IP sets and table rules", func() {
		t.pFilter.AwaitSet(Equal(localCIDRIPSet))
		t.pFilter.AwaitSet(Equal(remoteCIDRIPSet))

		t.pFilter.AwaitRule(t.tableType,
			constants.SmPostRoutingMssChain, And(
				ContainSubstring("\"ClampType\":%d", packetfilter.ToPMTU),
				ContainSubstring("\"SrcSetName\":%q", remoteCIDRIPSet),
				ContainSubstring("\"DestSetName\":%q", localCIDRIPSet)))
		t.pFilter.AwaitRule(t.tableType,
			constants.SmPostRoutingMssChain, And(
				ContainSubstring("\"ClampType\":%d", packetfilter.ToPMTU),
				ContainSubstring("\"SrcSetName\":%q", localCIDRIPSet),
				ContainSubstring("\"DestSetName\":%q", remoteCIDRIPSet)))
	})

	When("a local Endpoint is added and removed", func() {
		It("should add and remove IP set entries", func() {
			localEndpoint := newSubmEndpoint([]string{localIPv4Subnet1, localIPv6Subnet1, localIPv4Subnet2, localIPv6Subnet2})
			Expect(t.handler.LocalEndpointCreated(localEndpoint)).To(Succeed())

			for _, subnet := range localSubnets {
				t.pFilter.AwaitEntry(localCIDRIPSet, subnet)
			}

			t.pFilter.AwaitEntry(localCIDRIPSet, localCIDR)

			Expect(t.handler.LocalEndpointRemoved(localEndpoint)).To(Succeed())

			for _, subnet := range localEndpoint.Spec.Subnets {
				t.pFilter.AwaitNoEntry(localCIDRIPSet, subnet)
			}

			t.pFilter.AwaitNoEntry(localCIDRIPSet, localCIDR)
		})
	})

	When("a remote Endpoint is added and removed", func() {
		It("should add and remove IP set entries", func() {
			remoteEndpoint := newSubmEndpoint([]string{remoteIPv4Subnet1, remoteIPv4Subnet2, remoteIPv6Subnet1, remoteIPv6Subnet2})
			Expect(t.handler.RemoteEndpointCreated(remoteEndpoint)).To(Succeed())

			for _, subnet := range remoteSubnets {
				t.pFilter.AwaitEntry(remoteCIDRIPSet, subnet)
			}

			Expect(t.handler.RemoteEndpointRemoved(remoteEndpoint)).To(Succeed())

			for _, subnet := range remoteEndpoint.Spec.Subnets {
				t.pFilter.AwaitNoEntry(remoteCIDRIPSet, subnet)
			}
		})
	})

	Specify("Uninstall should remove IP sets and chains", func() {
		Expect(t.handler.Uninstall()).To(Succeed())

		t.pFilter.AwaitSetDeleted(localCIDRIPSet)
		t.pFilter.AwaitSetDeleted(remoteCIDRIPSet)
		t.pFilter.AwaitNoIPHookChain(packetfilter.ChainTypeRoute, Equal(constants.SmPostRoutingMssChain))
	})

	Specify("GetName should include the IP family", func() {
		Expect(t.handler.GetName()).To(ContainSubstring(string(t.ipFamily)))
	})
}

func newSubmEndpoint(subnets []string) *submV1.Endpoint {
	return &submV1.Endpoint{
		Spec: submV1.EndpointSpec{
			Subnets: subnets,
		},
	}
}
