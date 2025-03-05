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

var _ = When("a remote Endpoint is added", func() {
	var forwardHowManyFromLocal int

	t := newDiscoveryTestDriver()

	BeforeEach(func() {
		atomic.StoreInt64(&natdiscovery.RecheckTime, 0)
		atomic.StoreInt64(&natdiscovery.TotalTimeout, time.Hour.Nanoseconds())
		atomic.StoreInt64(&natdiscovery.PublicToPrivateFailoverTimeout, time.Hour.Nanoseconds())
		forwardHowManyFromLocal = 1
		t.remoteEndpoint.Spec.PublicIPs = []string{}
	})

	JustBeforeEach(func() {
		t.localND.ipv4Connection.forwardTo(t.remoteND.ipv4Connection, forwardHowManyFromLocal)
		t.localND.instance.AddEndpoint(&t.remoteEndpoint, k8snet.IPv4)
		t.localND.checkDiscovery()
	})

	Context("with only the private IP set", func() {
		t.testRemoteEndpointAdded(testRemotePrivateIP, natNotExpected)
	})

	Context("with only the public IP set", func() {
		BeforeEach(func() {
			t.remoteEndpoint.Spec.PublicIPs = []string{testRemotePublicIP}
			t.remoteEndpoint.Spec.PrivateIPs = []string{}
		})

		t.testRemoteEndpointAdded(testRemotePublicIP, natExpected)
	})

	Context("with both the public IP and private IP set", func() {
		var privateIPReq []byte
		var publicIPReq []byte

		BeforeEach(func() {
			forwardHowManyFromLocal = 0
			t.remoteEndpoint.Spec.PublicIPs = []string{testRemotePublicIP}
			t.remoteND.instance.AddEndpoint(&t.localEndpoint, k8snet.IPv4)
		})

		JustBeforeEach(func() {
			privateIPReq = t.localND.ipv4Connection.awaitSent()
			publicIPReq = t.localND.ipv4Connection.awaitSent()
		})

		Context("and the private IP responds after the public IP within the grace period", func() {
			It("should notify with the private IP NATEndpointInfo settings", func() {
				t.remoteND.ipv4Connection.inputFrom(publicIPReq, t.localND.ipv4Connection.addr)

				Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
					Endpoint:  t.remoteEndpoint,
					UseNAT:    true,
					UseIP:     t.remoteEndpoint.Spec.GetPublicIP(k8snet.IPv4),
					UseFamily: k8snet.IPv4,
				})))

				t.remoteND.ipv4Connection.inputFrom(privateIPReq, t.localND.ipv4Connection.addr)

				Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
					Endpoint:  t.remoteEndpoint,
					UseNAT:    false,
					UseIP:     t.remoteEndpoint.Spec.GetPrivateIP(k8snet.IPv4),
					UseFamily: k8snet.IPv4,
				})))
			})
		})

		Context("and the private IP responds after the public IP but after the grace period has elapsed", func() {
			It("should notify with the public IP NATEndpointInfo settings", func() {
				atomic.StoreInt64(&natdiscovery.PublicToPrivateFailoverTimeout, 0)

				t.remoteND.ipv4Connection.inputFrom(publicIPReq, t.localND.ipv4Connection.addr)

				Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
					Endpoint:  t.remoteEndpoint,
					UseNAT:    true,
					UseIP:     t.remoteEndpoint.Spec.GetPublicIP(k8snet.IPv4),
					UseFamily: k8snet.IPv4,
				})))

				t.remoteND.ipv4Connection.inputFrom(privateIPReq, t.localND.ipv4Connection.addr)

				Consistently(t.localND.instance.GetReadyChannel()).ShouldNot(Receive())
			})
		})

		Context("and the private IP responds first", func() {
			It("should notify with the private IP NATEndpointInfo settings", func() {
				t.remoteND.ipv4Connection.inputFrom(privateIPReq, t.localND.ipv4Connection.addr)

				Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
					Endpoint:  t.remoteEndpoint,
					UseNAT:    false,
					UseIP:     t.remoteEndpoint.Spec.GetPrivateIP(k8snet.IPv4),
					UseFamily: k8snet.IPv4,
				})))

				t.remoteND.ipv4Connection.inputFrom(publicIPReq, t.localND.ipv4Connection.addr)

				Consistently(t.localND.instance.GetReadyChannel()).ShouldNot(Receive())
			})
		})
	})

	Context("and the local Endpoint is not initially known to the remote process", func() {
		It("should notify with the correct NATEndpointInfo settings", func() {
			Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
				Endpoint:  t.remoteEndpoint,
				UseNAT:    false,
				UseIP:     t.remoteEndpoint.Spec.GetPrivateIP(k8snet.IPv4),
				UseFamily: k8snet.IPv4,
			})))
		})
	})

	Context("and then re-added after discovery is complete", func() {
		var newRemoteEndpoint submarinerv1.Endpoint

		BeforeEach(func() {
			t.remoteND.instance.AddEndpoint(&t.localEndpoint, k8snet.IPv4)
			newRemoteEndpoint = t.remoteEndpoint
		})

		JustBeforeEach(func() {
			Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive())

			t.remoteND.ipv4Connection.addr.IP = net.ParseIP(newRemoteEndpoint.Spec.GetPrivateIP(k8snet.IPv4))
			t.localND.ipv4Connection.forwardTo(t.remoteND.ipv4Connection, 1)

			t.localND.instance.AddEndpoint(&newRemoteEndpoint, k8snet.IPv4)
			t.localND.checkDiscovery()
		})

		Context("with no change to the Endpoint", func() {
			It("should notify with the original NATEndpointInfo settings", func() {
				Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
					Endpoint:  t.remoteEndpoint,
					UseNAT:    false,
					UseIP:     t.remoteEndpoint.Spec.GetPrivateIP(k8snet.IPv4),
					UseFamily: k8snet.IPv4,
				})))
			})
		})

		Context("with the Endpoint's private IP changed", func() {
			BeforeEach(func() {
				newRemoteEndpoint.Spec.PrivateIPs = []string{testRemotePrivateIP2}
				Expect(t.remoteND.localEndpoint.Update(context.Background(), func(existing *submarinerv1.EndpointSpec) {
					existing.PrivateIPs = []string{testRemotePrivateIP2}
				})).To(Succeed())
			})

			It("should notify with new NATEndpointInfo settings", func() {
				Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
					Endpoint:  newRemoteEndpoint,
					UseNAT:    false,
					UseIP:     newRemoteEndpoint.Spec.GetPrivateIP(k8snet.IPv4),
					UseFamily: k8snet.IPv4,
				})))
			})
		})
	})

	Context("and then re-added while discovery is in progress", func() {
		var newRemoteEndpoint submarinerv1.Endpoint

		BeforeEach(func() {
			forwardHowManyFromLocal = 0
			t.remoteND.instance.AddEndpoint(&t.localEndpoint, k8snet.IPv4)
			newRemoteEndpoint = t.remoteEndpoint
		})

		JustBeforeEach(func() {
			t.localND.instance.AddEndpoint(&newRemoteEndpoint, k8snet.IPv4)
		})

		Context("with no change to the Endpoint", func() {
			It("should not notify ready", func() {
				Consistently(t.localND.instance.GetReadyChannel()).ShouldNot(Receive())
			})
		})

		Context("with the Endpoint's private IP changed", func() {
			BeforeEach(func() {
				newRemoteEndpoint.Spec.PrivateIPs = []string{testRemotePrivateIP2}
				Expect(t.remoteND.localEndpoint.Update(context.Background(), func(existing *submarinerv1.EndpointSpec) {
					existing.PrivateIPs = []string{testRemotePrivateIP2}
				})).To(Succeed())
			})

			JustBeforeEach(func() {
				t.remoteND.ipv4Connection.addr.IP = net.ParseIP(newRemoteEndpoint.Spec.GetPrivateIP(k8snet.IPv4))
				t.localND.ipv4Connection.forwardTo(t.remoteND.ipv4Connection, -1)
				t.localND.checkDiscovery()
			})

			It("should notify with the correct NATEndpointInfo settings", func() {
				Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
					Endpoint:  newRemoteEndpoint,
					UseNAT:    false,
					UseIP:     newRemoteEndpoint.Spec.GetPrivateIP(k8snet.IPv4),
					UseFamily: k8snet.IPv4,
				})))
			})
		})
	})

	Context("and then removed while discovery is in progress", func() {
		BeforeEach(func() {
			forwardHowManyFromLocal = 0
		})

		It("should stop the discovery", func() {
			Expect(t.localND.ipv4Connection.udpSentChannel).To(Receive())
			Consistently(t.localND.instance.GetReadyChannel()).ShouldNot(Receive())

			t.localND.instance.RemoveEndpoint(t.remoteEndpoint.Spec.GetFamilyCableName(k8snet.IPv4))

			t.localND.checkDiscovery()
			Expect(t.localND.ipv4Connection.udpSentChannel).ToNot(Receive())
		})
	})

	Context("with no NAT discovery port set", func() {
		BeforeEach(func() {
			t.remoteEndpoint.Spec.PublicIPs = []string{testRemotePublicIP}
			t.remoteEndpoint.Spec.PrivateIPs = []string{}
			delete(t.remoteEndpoint.Spec.BackendConfig, submarinerv1.NATTDiscoveryPortConfig)
		})

		It("should notify with the legacy NATEndpointInfo settings", func() {
			Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
				Endpoint:  t.remoteEndpoint,
				UseNAT:    true,
				UseIP:     t.remoteEndpoint.Spec.GetPublicIP(k8snet.IPv4),
				UseFamily: k8snet.IPv4,
			})))
		})
	})

	Context("and the remote process doesn't respond", func() {
		BeforeEach(func() {
			forwardHowManyFromLocal = 0
			atomic.StoreInt64(&natdiscovery.TotalTimeout, (100 * time.Millisecond).Nanoseconds())
		})

		It("should eventually time out and notify with the legacy NATEndpointInfo settings", func() {
			// Drop the request sent out
			Expect(t.localND.ipv4Connection.udpSentChannel).Should(Receive())

			Consistently(t.localND.instance.GetReadyChannel(), natdiscovery.ToDuration(&natdiscovery.TotalTimeout)).ShouldNot(Receive())
			time.Sleep(50 * time.Millisecond)

			t.localND.checkDiscovery()
			Expect(t.localND.ipv4Connection.udpSentChannel).ToNot(Receive())

			Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
				Endpoint:  t.remoteEndpoint,
				UseNAT:    true,
				UseIP:     t.remoteEndpoint.Spec.GetPublicIP(k8snet.IPv4),
				UseFamily: k8snet.IPv4,
			})))
		})
	})
})

var _ = When(fmt.Sprintf("the %q config is invalid", submarinerv1.NATTDiscoveryPortConfig), func() {
	It("instantiation should return an error", func() {
		localEndpoint := createTestLocalEndpoint()
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

func newDiscoveryTestDriver() *discoveryTestDriver {
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

		t.remoteEndpoint = createTestRemoteEndpoint()
		t.localEndpoint = createTestLocalEndpoint()

		t.localND = newNATDiscovery(&t.localEndpoint, &net.UDPAddr{
			IP:   net.ParseIP(testLocalPrivateIP),
			Port: int(testLocalNATPort),
		})

		t.remoteND = newNATDiscovery(&t.remoteEndpoint, &net.UDPAddr{
			IP:   net.ParseIP(testRemotePrivateIP),
			Port: int(testRemoteNATPort),
		})

		t.remoteND.ipv4Connection.forwardTo(t.localND.ipv4Connection, -1)
	})

	return t
}

func (t *discoveryTestDriver) testRemoteEndpointAdded(expIP string, expectNAT bool) {
	BeforeEach(func() {
		t.remoteND.instance.AddEndpoint(&t.localEndpoint, k8snet.IPv4)
	})

	It("should notify with the correct NATEndpointInfo settings and stop the discovery", func() {
		Eventually(t.localND.instance.GetReadyChannel(), 5).Should(Receive(Equal(&natdiscovery.NATEndpointInfo{
			Endpoint:  t.remoteEndpoint,
			UseNAT:    expectNAT,
			UseIP:     expIP,
			UseFamily: k8snet.IPv4,
		})))

		// Verify it doesn't time out and try to notify of the legacy settings

		atomic.StoreInt64(&natdiscovery.TotalTimeout, (100 * time.Millisecond).Nanoseconds())
		time.Sleep(natdiscovery.ToDuration(&natdiscovery.TotalTimeout) + 20)

		t.localND.checkDiscovery()
		Expect(t.localND.ipv4Connection.udpSentChannel).ToNot(Receive())
		Consistently(t.localND.instance.GetReadyChannel()).ShouldNot(Receive())

		// Verify it doesn't try to send another request after the recheck time period has elapsed

		atomic.StoreInt64(&natdiscovery.TotalTimeout, time.Hour.Nanoseconds())

		t.localND.checkDiscovery()
		Expect(t.localND.ipv4Connection.udpSentChannel).ToNot(Receive())
		Consistently(t.localND.instance.GetReadyChannel()).ShouldNot(Receive())
	})
}
