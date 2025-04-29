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

package natdiscovery_test

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submendpoint "github.com/submariner-io/submariner/pkg/endpoint"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	k8snet "k8s.io/utils/net"
)

const (
	natExpected    = true
	natNotExpected = false
)

var _ = When("IPv4 remote Endpoint is added in IPv4 env", func() {
	t := newDiscoveryTestDriver(true, false)

	addEndpointDiscoveryTests(
		k8snet.IPv4,
		t,
		func() string { return testRemotePrivateIP },
		func() string { return testRemotePublicIP },
		func() string { return testRemotePrivateIP2 },
		func(nd *NATDiscoveryInfo) *FakeServerConnection { return nd.ipv4Connection },
	)
})

var _ = When("IPv4 remote Endpoint is added in dualstack env", func() {
	t := newDiscoveryTestDriver(true, true)

	addEndpointDiscoveryTests(
		k8snet.IPv4,
		t,
		func() string { return testRemotePrivateIP },
		func() string { return testRemotePublicIP },
		func() string { return testRemotePrivateIP2 },
		func(nd *NATDiscoveryInfo) *FakeServerConnection { return nd.ipv4Connection },
	)
})

var _ = When("IPv6 remote Endpoint is added in IPv6 env", func() {
	t := newDiscoveryTestDriver(false, true)

	addEndpointDiscoveryTests(
		k8snet.IPv6,
		t,
		func() string { return testRemotePrivateIPv6 },
		func() string { return testRemotePublicIPv6 },
		func() string { return testRemotePrivateIPv62 },
		func(nd *NATDiscoveryInfo) *FakeServerConnection { return nd.ipv6Connection },
	)
})

var _ = When("IPv6 remote Endpoint is added in dualstack env", func() {
	t := newDiscoveryTestDriver(true, true)

	addEndpointDiscoveryTests(
		k8snet.IPv6,
		t,
		func() string { return testRemotePrivateIPv6 },
		func() string { return testRemotePublicIPv6 },
		func() string { return testRemotePrivateIPv62 },
		func(nd *NATDiscoveryInfo) *FakeServerConnection { return nd.ipv6Connection },
	)
})

func testPublicOnly(t *discoveryTestDriver, getPublicIP func() string) {
	Context("with only the public IP set", func() {
		BeforeEach(func() {
			t.remoteEndpoint.Spec.PublicIPs = []string{getPublicIP()}
			t.remoteEndpoint.Spec.PrivateIPs = []string{}
		})

		t.testRemoteEndpointAdded(getPublicIP(), natExpected)
	})
}

func testPrivateOnly(t *discoveryTestDriver, getPrivateIP func() string) {
	Context("with only the private IP set", func() {
		t.testRemoteEndpointAdded(getPrivateIP(), natNotExpected)
	})
}

func testResponseOrdering(ipFamily k8snet.IPFamily, t *discoveryTestDriver,
	getPrivateIP, getPublicIP func() string,
	getConnection func(*NATDiscoveryInfo) *FakeServerConnection,
	forwardHowManyFromLocal *int,
) {
	var privateIPReq, publicIPReq []byte

	Context("with both the public IP and private IP set", func() {
		BeforeEach(func() {
			*forwardHowManyFromLocal = 0
			t.remoteEndpoint.Spec.PublicIPs = []string{getPublicIP()}
			t.remoteND.instance.AddEndpoint(&t.localEndpoint, ipFamily)
		})

		JustBeforeEach(func() {
			conn := getConnection(t.localND)
			privateIPReq = conn.awaitSent()
			publicIPReq = conn.awaitSent()
		})

		Context("and the private IP responds after the public IP within the grace period", func() {
			It("should notify with the private IP NATEndpointInfo settings", func() {
				getConnection(t.remoteND).inputFrom(publicIPReq, getConnection(t.localND).addr)

				Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
					Endpoint:  t.remoteEndpoint,
					UseNAT:    true,
					UseIP:     getPublicIP(),
					UseFamily: ipFamily,
				})))

				getConnection(t.remoteND).inputFrom(privateIPReq, getConnection(t.localND).addr)

				Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
					Endpoint:  t.remoteEndpoint,
					UseNAT:    false,
					UseIP:     getPrivateIP(),
					UseFamily: ipFamily,
				})))
			})
		})

		Context("and the private IP responds after the public IP but after the grace period has elapsed", func() {
			It("should notify with the public IP NATEndpointInfo settings", func() {
				atomic.StoreInt64(&natdiscovery.PublicToPrivateFailoverTimeout, 0)

				getConnection(t.remoteND).inputFrom(publicIPReq, getConnection(t.localND).addr)

				Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
					Endpoint:  t.remoteEndpoint,
					UseNAT:    true,
					UseIP:     t.remoteEndpoint.Spec.GetPublicIP(ipFamily),
					UseFamily: ipFamily,
				})))

				getConnection(t.remoteND).inputFrom(privateIPReq, getConnection(t.localND).addr)

				Consistently(t.localND.instance.GetReadyChannel()).ShouldNot(Receive())
			})
		})

		Context("and the private IP responds first", func() {
			It("should notify with the private IP NATEndpointInfo settings", func() {
				getConnection(t.remoteND).inputFrom(privateIPReq, getConnection(t.localND).addr)

				Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
					Endpoint:  t.remoteEndpoint,
					UseNAT:    false,
					UseIP:     t.remoteEndpoint.Spec.GetPrivateIP(ipFamily),
					UseFamily: ipFamily,
				})))

				getConnection(t.remoteND).inputFrom(publicIPReq, getConnection(t.localND).addr)

				Consistently(t.localND.instance.GetReadyChannel()).ShouldNot(Receive())
			})
		})
	})

	Context("and the local Endpoint is not initially known to the remote process", func() {
		It("should notify with the correct NATEndpointInfo settings", func() {
			Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
				Endpoint:  t.remoteEndpoint,
				UseNAT:    false,
				UseIP:     t.remoteEndpoint.Spec.GetPrivateIP(ipFamily),
				UseFamily: ipFamily,
			})))
		})
	})
}

func testReAddingEndpointAfterDiscoveryComplete(ipFamily k8snet.IPFamily, t *discoveryTestDriver,
	getPrivateIP2 func() string,
	getConnection func(*NATDiscoveryInfo) *FakeServerConnection,
) {
	Context("and then re-added after discovery is complete", func() {
		var newRemoteEndpoint submarinerv1.Endpoint

		BeforeEach(func() {
			t.remoteND.instance.AddEndpoint(&t.localEndpoint, ipFamily)
			newRemoteEndpoint = t.remoteEndpoint
		})

		JustBeforeEach(func() {
			Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive())

			getConnection(t.remoteND).addr.IP = net.ParseIP(newRemoteEndpoint.Spec.GetPrivateIP(ipFamily))
			getConnection(t.localND).forwardTo(getConnection(t.remoteND), 1)

			t.localND.instance.AddEndpoint(&newRemoteEndpoint, ipFamily)
			t.localND.checkDiscovery()
		})

		Context("with no change to the Endpoint", func() {
			It("should notify with the original NATEndpointInfo settings", func() {
				Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
					Endpoint:  t.remoteEndpoint,
					UseNAT:    false,
					UseIP:     t.remoteEndpoint.Spec.GetPrivateIP(ipFamily),
					UseFamily: ipFamily,
				})))
			})
		})

		Context("with the Endpoint's private IP changed", func() {
			BeforeEach(func() {
				newRemoteEndpoint.Spec.PrivateIPs = []string{getPrivateIP2()}

				Expect(t.remoteND.localEndpoint.Update(context.Background(), func(existing *submarinerv1.EndpointSpec) {
					existing.PrivateIPs = []string{getPrivateIP2()}
				})).To(Succeed())
			})

			It("should notify with new NATEndpointInfo settings", func() {
				Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
					Endpoint:  newRemoteEndpoint,
					UseNAT:    false,
					UseIP:     newRemoteEndpoint.Spec.GetPrivateIP(ipFamily),
					UseFamily: ipFamily,
				})))
			})
		})
	})
}

func testReAddingEndpointDuringDiscovery(ipFamily k8snet.IPFamily, t *discoveryTestDriver,
	getPrivateIP2 func() string,
	getConnection func(*NATDiscoveryInfo) *FakeServerConnection,
	forwardHowManyFromLocal *int,
) {
	Context("and then re-added while discovery is in progress", func() {
		var newRemoteEndpoint submarinerv1.Endpoint

		BeforeEach(func() {
			*forwardHowManyFromLocal = 0

			t.remoteND.instance.AddEndpoint(&t.localEndpoint, ipFamily)
			newRemoteEndpoint = t.remoteEndpoint
		})

		JustBeforeEach(func() {
			t.localND.instance.AddEndpoint(&newRemoteEndpoint, ipFamily)
		})

		Context("with no change to the Endpoint", func() {
			It("should not notify ready", func() {
				Consistently(t.localND.instance.GetReadyChannel()).ShouldNot(Receive())
			})
		})

		Context("with the Endpoint's private IP changed", func() {
			BeforeEach(func() {
				newRemoteEndpoint.Spec.PrivateIPs = []string{getPrivateIP2()}

				Expect(t.remoteND.localEndpoint.Update(context.Background(), func(existing *submarinerv1.EndpointSpec) {
					existing.PrivateIPs = []string{getPrivateIP2()}
				})).To(Succeed())
			})

			JustBeforeEach(func() {
				getConnection(t.remoteND).addr.IP = net.ParseIP(newRemoteEndpoint.Spec.GetPrivateIP(ipFamily))
				getConnection(t.localND).forwardTo(getConnection(t.remoteND), -1)
				t.localND.checkDiscovery()
			})

			It("should notify with the correct NATEndpointInfo settings", func() {
				Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
					Endpoint:  newRemoteEndpoint,
					UseNAT:    false,
					UseIP:     newRemoteEndpoint.Spec.GetPrivateIP(ipFamily),
					UseFamily: ipFamily,
				})))
			})
		})
	})

	Context("and then removed while discovery is in progress", func() {
		BeforeEach(func() {
			*forwardHowManyFromLocal = 0
		})

		It("should stop the discovery", func() {
			Expect(getConnection(t.localND).udpSentChannel).To(Receive())
			Consistently(t.localND.instance.GetReadyChannel()).ShouldNot(Receive())

			t.localND.instance.RemoveEndpoint(t.remoteEndpoint.Spec.GetFamilyCableName(ipFamily))

			t.localND.checkDiscovery()
			Expect(getConnection(t.localND).udpSentChannel).ToNot(Receive())
		})
	})
}

func testNoNatPortSet(ipFamily k8snet.IPFamily, t *discoveryTestDriver, getPublicIP func() string) {
	Context("with no NAT discovery port set", func() {
		BeforeEach(func() {
			t.remoteEndpoint.Spec.PublicIPs = []string{getPublicIP()}
			t.remoteEndpoint.Spec.PrivateIPs = []string{}
			delete(t.remoteEndpoint.Spec.BackendConfig, submarinerv1.NATTDiscoveryPortConfig)
		})

		It("should notify with the legacy NATEndpointInfo settings", func() {
			Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
				Endpoint:  t.remoteEndpoint,
				UseNAT:    true,
				UseIP:     t.remoteEndpoint.Spec.GetPublicIP(ipFamily),
				UseFamily: ipFamily,
			})))
		})
	})
}

func testTimeoutBehavior(ipFamily k8snet.IPFamily, t *discoveryTestDriver,
	getConnection func(*NATDiscoveryInfo) *FakeServerConnection,
	forwardHowManyFromLocal *int,
) {
	Context("and the remote process doesn't respond", func() {
		BeforeEach(func() {
			*forwardHowManyFromLocal = 0

			atomic.StoreInt64(&natdiscovery.TotalTimeout, (100 * time.Millisecond).Nanoseconds())
		})

		It("should eventually time out and notify with the legacy NATEndpointInfo settings", func() {
			// Drop the request sent out
			Expect(getConnection(t.localND).udpSentChannel).Should(Receive())

			Consistently(t.localND.instance.GetReadyChannel(), natdiscovery.ToDuration(&natdiscovery.TotalTimeout)).ShouldNot(Receive())
			time.Sleep(50 * time.Millisecond)

			t.localND.checkDiscovery()
			Expect(getConnection(t.localND).udpSentChannel).ToNot(Receive())

			Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
				Endpoint:  t.remoteEndpoint,
				UseNAT:    true,
				UseIP:     t.remoteEndpoint.Spec.GetPublicIP(ipFamily),
				UseFamily: ipFamily,
			})))
		})
	})
}

func addEndpointDiscoveryTests(ipFamily k8snet.IPFamily, t *discoveryTestDriver,
	getPrivateIP, getPublicIP, getPrivateIP2 func() string,
	getConnection func(*NATDiscoveryInfo) *FakeServerConnection,
) {
	var forwardHowManyFromLocal int

	BeforeEach(func() {
		atomic.StoreInt64(&natdiscovery.RecheckTime, 0)
		atomic.StoreInt64(&natdiscovery.TotalTimeout, time.Hour.Nanoseconds())
		atomic.StoreInt64(&natdiscovery.PublicToPrivateFailoverTimeout, time.Hour.Nanoseconds())

		forwardHowManyFromLocal = 1

		t.remoteEndpoint.Spec.PublicIPs = []string{}
	})

	JustBeforeEach(func() {
		getConnection(t.localND).forwardTo(getConnection(t.remoteND), forwardHowManyFromLocal)
		t.localND.instance.AddEndpoint(&t.remoteEndpoint, ipFamily)
		t.localND.checkDiscovery()
	})

	testPublicOnly(t, getPublicIP)
	testPrivateOnly(t, getPrivateIP)
	testResponseOrdering(ipFamily, t,
		getPrivateIP, getPublicIP,
		getConnection,
		&forwardHowManyFromLocal,
	)

	testReAddingEndpointAfterDiscoveryComplete(ipFamily, t,
		getPrivateIP2,
		getConnection,
	)

	testReAddingEndpointDuringDiscovery(ipFamily, t,
		getPrivateIP2,
		getConnection,
		&forwardHowManyFromLocal,
	)

	testNoNatPortSet(ipFamily, t, getPublicIP)

	testTimeoutBehavior(ipFamily, t,
		getConnection,
		&forwardHowManyFromLocal)
}

var _ = When(fmt.Sprintf("the %q config is invalid", submarinerv1.NATTDiscoveryPortConfig), func() {
	It("instantiation should return an error", func() {
		localEndpoint := createTestLocalEndpoint(true, false)
		localEndpoint.Spec.BackendConfig[submarinerv1.NATTDiscoveryPortConfig] = "bogus"

		_, err := natdiscovery.New(submendpoint.NewLocal(&localEndpoint.Spec, dynamicfake.NewSimpleDynamicClient(scheme.Scheme), ""))
		Expect(err).To(HaveOccurred())
	})
})

type discoveryTestDriver struct {
	localND        *NATDiscoveryInfo
	localEndpoint  submarinerv1.Endpoint
	remoteND       *NATDiscoveryInfo
	remoteEndpoint submarinerv1.Endpoint
}

func newDiscoveryTestDriver(isIPv4, isIPv6 bool) *discoveryTestDriver {
	t := &discoveryTestDriver{}

	BeforeEach(func() {
		oldRecheckTime := atomic.LoadInt64(&natdiscovery.RecheckTime)
		oldTotalTimeout := atomic.LoadInt64(&natdiscovery.TotalTimeout)
		oldPublicToPrivateFailoverTimeout := atomic.LoadInt64(&natdiscovery.PublicToPrivateFailoverTimeout)

		DeferCleanup(func() {
			atomic.StoreInt64(&natdiscovery.RecheckTime, oldRecheckTime)
			atomic.StoreInt64(&natdiscovery.TotalTimeout, oldTotalTimeout)
			atomic.StoreInt64(&natdiscovery.PublicToPrivateFailoverTimeout, oldPublicToPrivateFailoverTimeout)
		})

		var ipv4AddrL, ipv4AddrR *net.UDPAddr
		var ipv6AddrL, ipv6AddrR *net.UDPAddr

		if isIPv4 {
			ipv4AddrL = &net.UDPAddr{
				IP:   net.ParseIP(testLocalPrivateIP),
				Port: int(testLocalNATPort),
			}
			ipv4AddrR = &net.UDPAddr{
				IP:   net.ParseIP(testRemotePrivateIP),
				Port: int(testRemoteNATPort),
			}
		}

		if isIPv6 {
			ipv6AddrL = &net.UDPAddr{
				IP:   net.ParseIP(testLocalPrivateIPv6),
				Port: int(testLocalNATPort),
			}
			ipv6AddrR = &net.UDPAddr{
				IP:   net.ParseIP(testRemotePrivateIPv6),
				Port: int(testRemoteNATPort),
			}
		}

		t.remoteEndpoint = createTestRemoteEndpoint(isIPv4, isIPv6)
		t.localEndpoint = createTestLocalEndpoint(isIPv4, isIPv6)

		t.localND = newNATDiscovery(&t.localEndpoint, ipv4AddrL, ipv6AddrL)
		t.remoteND = newNATDiscovery(&t.remoteEndpoint, ipv4AddrR, ipv6AddrR)

		if t.remoteND.ipv4Connection != nil {
			t.remoteND.ipv4Connection.forwardTo(t.localND.ipv4Connection, -1)
		}

		if t.remoteND.ipv6Connection != nil {
			t.remoteND.ipv6Connection.forwardTo(t.localND.ipv6Connection, -1)
		}
	})

	return t
}

func (t *discoveryTestDriver) testRemoteEndpointAdded(expIP string, expectNAT bool) {
	BeforeEach(func() {
		t.remoteND.instance.AddEndpoint(&t.localEndpoint, k8snet.IPFamilyOfString(expIP))
	})

	It("should notify with the correct NATEndpointInfo settings and stop the discovery", func() {
		Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
			Endpoint:  t.remoteEndpoint,
			UseNAT:    expectNAT,
			UseIP:     expIP,
			UseFamily: k8snet.IPFamilyOfString(expIP),
		})))

		// Verify it doesn't time out and try to notify of the legacy settings

		atomic.StoreInt64(&natdiscovery.TotalTimeout, (100 * time.Millisecond).Nanoseconds())
		time.Sleep(natdiscovery.ToDuration(&natdiscovery.TotalTimeout) + 20)

		t.localND.checkDiscovery()

		if k8snet.IPFamilyOfString(expIP) == k8snet.IPv4 {
			Expect(t.localND.ipv4Connection.udpSentChannel).ToNot(Receive())
		} else {
			Expect(t.localND.ipv6Connection.udpSentChannel).ToNot(Receive())
		}

		Consistently(t.localND.instance.GetReadyChannel()).ShouldNot(Receive())

		// Verify it doesn't try to send another request after the recheck time period has elapsed

		atomic.StoreInt64(&natdiscovery.TotalTimeout, time.Hour.Nanoseconds())

		t.localND.checkDiscovery()

		if k8snet.IPFamilyOfString(expIP) == k8snet.IPv4 {
			Expect(t.localND.ipv4Connection.udpSentChannel).ToNot(Receive())
		} else {
			Expect(t.localND.ipv6Connection.udpSentChannel).ToNot(Receive())
		}

		Consistently(t.localND.instance.GetReadyChannel()).ShouldNot(Receive())
	})
}
