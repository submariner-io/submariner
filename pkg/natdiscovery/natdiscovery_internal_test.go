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

package natdiscovery

import (
	"net"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

const (
	natExpected    = true
	natNotExpected = false
)

var _ = When("a remote Endpoint is added", func() {
	var forwardHowManyFromLocal int

	t := newDiscoveryTestDriver()

	BeforeEach(func() {
		atomic.StoreInt64(&recheckTime, 0)
		atomic.StoreInt64(&totalTimeout, time.Hour.Nanoseconds())
		atomic.StoreInt64(&publicToPrivateFailoverTimeout, time.Hour.Nanoseconds())
		forwardHowManyFromLocal = 1
		t.remoteEndpoint.Spec.PublicIP = ""
	})

	JustBeforeEach(func() {
		forwardFromUDPChan(t.localUDPSent, t.localUDPAddr, t.remoteND, forwardHowManyFromLocal)
		t.localND.AddEndpoint(&t.remoteEndpoint)
		t.localND.checkEndpointList()
	})

	Context("with only the private IP set", func() {
		t.testRemoteEndpointAdded(testRemotePrivateIP, natNotExpected)
	})

	Context("with only the public IP set", func() {
		BeforeEach(func() {
			t.remoteEndpoint.Spec.PublicIP = testRemotePublicIP
			t.remoteEndpoint.Spec.PrivateIP = ""
		})

		t.testRemoteEndpointAdded(testRemotePublicIP, natExpected)
	})

	Context("with both the public IP and private IP set", func() {
		var privateIPReq []byte
		var publicIPReq []byte

		BeforeEach(func() {
			forwardHowManyFromLocal = 0
			t.remoteEndpoint.Spec.PublicIP = testRemotePublicIP
			t.remoteND.AddEndpoint(&t.localEndpoint)
		})

		JustBeforeEach(func() {
			privateIPReq = awaitChan(t.localUDPSent)
			publicIPReq = awaitChan(t.localUDPSent)
		})

		Context("and the private IP responds after the public IP within the grace period", func() {
			It("should notify with the private IP NATEndpointInfo settings", func() {
				Expect(t.remoteND.parseAndHandleMessageFromAddress(publicIPReq, t.localUDPAddr))

				Eventually(t.readyChannel, 5).Should(Receive(Equal(&NATEndpointInfo{
					Endpoint: t.remoteEndpoint,
					UseNAT:   true,
					UseIP:    t.remoteEndpoint.Spec.PublicIP,
				})))

				Expect(t.remoteND.parseAndHandleMessageFromAddress(privateIPReq, t.localUDPAddr))

				Eventually(t.readyChannel, 5).Should(Receive(Equal(&NATEndpointInfo{
					Endpoint: t.remoteEndpoint,
					UseNAT:   false,
					UseIP:    t.remoteEndpoint.Spec.PrivateIP,
				})))
			})
		})

		Context("and the private IP responds after the public IP but after the grace period has elapsed", func() {
			It("should notify with the public IP NATEndpointInfo settings", func() {
				atomic.StoreInt64(&publicToPrivateFailoverTimeout, 0)

				Expect(t.remoteND.parseAndHandleMessageFromAddress(publicIPReq, t.localUDPAddr))

				Eventually(t.readyChannel, 5).Should(Receive(Equal(&NATEndpointInfo{
					Endpoint: t.remoteEndpoint,
					UseNAT:   true,
					UseIP:    t.remoteEndpoint.Spec.PublicIP,
				})))

				Expect(t.remoteND.parseAndHandleMessageFromAddress(privateIPReq, t.localUDPAddr))

				Consistently(t.readyChannel).ShouldNot(Receive())
			})
		})

		Context("and the private IP responds first", func() {
			It("should notify with the private IP NATEndpointInfo settings", func() {
				Expect(t.remoteND.parseAndHandleMessageFromAddress(privateIPReq, t.localUDPAddr))

				Eventually(t.readyChannel, 5).Should(Receive(Equal(&NATEndpointInfo{
					Endpoint: t.remoteEndpoint,
					UseNAT:   false,
					UseIP:    t.remoteEndpoint.Spec.PrivateIP,
				})))

				Expect(t.remoteND.parseAndHandleMessageFromAddress(publicIPReq, t.localUDPAddr))

				Consistently(t.readyChannel).ShouldNot(Receive())
			})
		})
	})

	Context("and the local Endpoint is not initially known to the remote process", func() {
		It("should notify with the correct NATEndpointInfo settings", func() {
			Eventually(t.readyChannel, 5).Should(Receive(Equal(&NATEndpointInfo{
				Endpoint: t.remoteEndpoint,
				UseNAT:   false,
				UseIP:    t.remoteEndpoint.Spec.PrivateIP,
			})))
		})
	})

	Context("and then re-added after discovery is complete", func() {
		var newRemoteEndpoint submarinerv1.Endpoint

		BeforeEach(func() {
			t.remoteND.AddEndpoint(&t.localEndpoint)
			newRemoteEndpoint = t.remoteEndpoint
		})

		JustBeforeEach(func() {
			Eventually(t.readyChannel, 5).Should(Receive())

			t.remoteUDPAddr.IP = net.ParseIP(newRemoteEndpoint.Spec.PrivateIP)
			forwardFromUDPChan(t.localUDPSent, t.localUDPAddr, t.remoteND, 1)

			t.localND.AddEndpoint(&newRemoteEndpoint)
			t.localND.checkEndpointList()
		})

		Context("with no change to the Endpoint", func() {
			It("should notify with the original NATEndpointInfo settings", func() {
				Eventually(t.readyChannel, 5).Should(Receive(Equal(&NATEndpointInfo{
					Endpoint: t.remoteEndpoint,
					UseNAT:   false,
					UseIP:    t.remoteEndpoint.Spec.PrivateIP,
				})))
			})
		})

		Context("with the Endpoint's private IP changed", func() {
			BeforeEach(func() {
				newRemoteEndpoint.Spec.PrivateIP = testRemotePrivateIP2
				t.remoteND.localEndpoint.Spec.PrivateIP = testRemotePrivateIP2
			})

			It("should notify with new NATEndpointInfo settings", func() {
				Eventually(t.readyChannel, 5).Should(Receive(Equal(&NATEndpointInfo{
					Endpoint: newRemoteEndpoint,
					UseNAT:   false,
					UseIP:    newRemoteEndpoint.Spec.PrivateIP,
				})))
			})
		})
	})

	Context("and then re-added while discovery is in progress", func() {
		var newRemoteEndpoint submarinerv1.Endpoint

		BeforeEach(func() {
			forwardHowManyFromLocal = 0
			t.remoteND.AddEndpoint(&t.localEndpoint)
			newRemoteEndpoint = t.remoteEndpoint
		})

		JustBeforeEach(func() {
			t.localND.AddEndpoint(&newRemoteEndpoint)
		})

		Context("with no change to the Endpoint", func() {
			It("should not notify ready", func() {
				Consistently(t.readyChannel).ShouldNot(Receive())
			})
		})

		Context("with the Endpoint's private IP changed", func() {
			BeforeEach(func() {
				newRemoteEndpoint.Spec.PrivateIP = testRemotePrivateIP2
				t.remoteND.localEndpoint.Spec.PrivateIP = testRemotePrivateIP2
			})

			JustBeforeEach(func() {
				t.remoteUDPAddr.IP = net.ParseIP(newRemoteEndpoint.Spec.PrivateIP)
				forwardFromUDPChan(t.localUDPSent, t.localUDPAddr, t.remoteND, -1)
				t.localND.checkEndpointList()
			})

			It("should notify with the correct NATEndpointInfo settings", func() {
				Eventually(t.readyChannel, 5).Should(Receive(Equal(&NATEndpointInfo{
					Endpoint: newRemoteEndpoint,
					UseNAT:   false,
					UseIP:    newRemoteEndpoint.Spec.PrivateIP,
				})))
			})
		})
	})

	Context("and then removed while discovery is in progress", func() {
		BeforeEach(func() {
			forwardHowManyFromLocal = 0
		})

		It("should stop the discovery", func() {
			Expect(t.localUDPSent).To(Receive())
			Consistently(t.readyChannel).ShouldNot(Receive())

			t.localND.RemoveEndpoint(t.remoteEndpoint.Spec.CableName)

			t.localND.checkEndpointList()
			Expect(t.localUDPSent).ToNot(Receive())
		})
	})

	Context("with no NAT discovery port set", func() {
		BeforeEach(func() {
			t.remoteEndpoint.Spec.PublicIP = testRemotePublicIP
			t.remoteEndpoint.Spec.PrivateIP = ""
			delete(t.remoteEndpoint.Spec.BackendConfig, submarinerv1.NATTDiscoveryPortConfig)
		})

		It("should notify with the legacy NATEndpointInfo settings", func() {
			Eventually(t.readyChannel, 5).Should(Receive(Equal(&NATEndpointInfo{
				Endpoint: t.remoteEndpoint,
				UseNAT:   true,
				UseIP:    t.remoteEndpoint.Spec.PublicIP,
			})))
		})
	})

	Context("and the remote process doesn't respond", func() {
		BeforeEach(func() {
			forwardHowManyFromLocal = 0
			atomic.StoreInt64(&totalTimeout, (100 * time.Millisecond).Nanoseconds())
		})

		It("should eventually time out and notify with the legacy NATEndpointInfo settings", func() {
			// Drop the request sent out
			Expect(t.localUDPSent).Should(Receive())

			Consistently(t.readyChannel, toDuration(&totalTimeout)).ShouldNot(Receive())
			time.Sleep(50 * time.Millisecond)

			t.localND.checkEndpointList()
			Expect(t.localUDPSent).ToNot(Receive())

			Eventually(t.readyChannel, 5).Should(Receive(Equal(&NATEndpointInfo{
				Endpoint: t.remoteEndpoint,
				UseNAT:   true,
				UseIP:    t.remoteEndpoint.Spec.PublicIP,
			})))
		})
	})
})

type discoveryTestDriver struct {
	localND                           *natDiscovery
	localUDPSent                      chan []byte
	localEndpoint                     submarinerv1.Endpoint
	localUDPAddr                      *net.UDPAddr
	remoteND                          *natDiscovery
	remoteUDPSent                     chan []byte
	remoteEndpoint                    submarinerv1.Endpoint
	remoteUDPAddr                     *net.UDPAddr
	readyChannel                      chan *NATEndpointInfo
	oldRecheckTime                    int64
	oldTotalTimeout                   int64
	oldPublicToPrivateFailoverTimeout int64
}

func newDiscoveryTestDriver() *discoveryTestDriver {
	t := &discoveryTestDriver{}

	BeforeEach(func() {
		t.oldRecheckTime = atomic.LoadInt64(&recheckTime)
		t.oldTotalTimeout = atomic.LoadInt64(&totalTimeout)
		t.oldPublicToPrivateFailoverTimeout = atomic.LoadInt64(&publicToPrivateFailoverTimeout)

		t.localUDPAddr = &net.UDPAddr{
			IP:   net.ParseIP(testLocalPrivateIP),
			Port: int(testLocalNATPort),
		}

		t.remoteUDPAddr = &net.UDPAddr{
			IP:   net.ParseIP(testRemotePrivateIP),
			Port: int(testRemoteNATPort),
		}

		t.remoteEndpoint = createTestRemoteEndpoint()
		t.localEndpoint = createTestLocalEndpoint()

		t.localND, t.localUDPSent, t.readyChannel = createTestListener(&t.localEndpoint)
		t.localND.findSrcIP = func(_ string) string { return testLocalPrivateIP }

		t.remoteND, t.remoteUDPSent, _ = createTestListener(&t.remoteEndpoint)
		t.remoteND.findSrcIP = func(_ string) string { return testRemotePrivateIP }

		forwardFromUDPChan(t.remoteUDPSent, t.remoteUDPAddr, t.localND, -1)
	})

	AfterEach(func() {
		close(t.localUDPSent)
		close(t.remoteUDPSent)

		atomic.StoreInt64(&recheckTime, t.oldRecheckTime)
		atomic.StoreInt64(&totalTimeout, t.oldTotalTimeout)
		atomic.StoreInt64(&publicToPrivateFailoverTimeout, t.oldPublicToPrivateFailoverTimeout)
	})

	return t
}

func (t *discoveryTestDriver) testRemoteEndpointAdded(expIP string, expectNAT bool) {
	BeforeEach(func() {
		t.remoteND.AddEndpoint(&t.localEndpoint)
	})

	It("should notify with the correct NATEndpointInfo settings and stop the discovery", func() {
		Eventually(t.readyChannel, 5).Should(Receive(Equal(&NATEndpointInfo{
			Endpoint: t.remoteEndpoint,
			UseNAT:   expectNAT,
			UseIP:    expIP,
		})))

		// Verify it doesn't time out and try to notify of the legacy settings

		atomic.StoreInt64(&totalTimeout, (100 * time.Millisecond).Nanoseconds())
		time.Sleep(toDuration(&totalTimeout) + 20)

		t.localND.checkEndpointList()
		Expect(t.localUDPSent).ToNot(Receive())
		Consistently(t.readyChannel).ShouldNot(Receive())

		// Verify it doesn't try to send another request after the recheck time period has elapsed

		atomic.StoreInt64(&totalTimeout, time.Hour.Nanoseconds())

		t.localND.checkEndpointList()
		Expect(t.localUDPSent).ToNot(Receive())
		Consistently(t.readyChannel).ShouldNot(Receive())
	})
}
