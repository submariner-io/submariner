/*
© 2021 Red Hat, Inc. and others

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
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("remoteEndpointNAT", func() {

	var rnat *remoteEndpointNAT
	var readyChannel chan *NATEndpointInfo

	BeforeEach(func() {
		readyChannel = make(chan *NATEndpointInfo, 10)
		remoteEndpoint := createTestRemoteEndpoint()
		rnat = newRemoteEndpointNAT(&remoteEndpoint, readyChannel)
	})

	verifyReady := func() {
		It("should notify ready", func() {
			Expect(readyChannel).To(Receive(Equal(&NATEndpointInfo{
				Endpoint: rnat.endpoint.Spec,
				UseIP:    rnat.useIP,
				UseNAT:   rnat.useNAT,
			})))

			rnat.transitionToState(rnat.state)
			Expect(readyChannel).ToNot(Receive())
		})
	}

	verifyNotReady := func() {
		It("should not notify ready", func() {
			Expect(readyChannel).ToNot(Receive())
		})
	}

	When("first created", func() {
		It("shouldCheck should return true", func() {
			Expect(rnat.shouldCheck()).To(BeTrue())
		})
	})

	When("right after a check has been sent", func() {
		It("shouldCheck should return false", func() {
			rnat.checkSent()
			Expect(rnat.shouldCheck()).To(BeFalse())
		})
	})

	When("the total timeout has elapsed", func() {
		It("should report as timed out", func() {
			rnat.started = time.Now().Add(-toDuration(&totalTimeout))
			Expect(rnat.hasTimedOut()).To(BeTrue())
		})
	})

	When("using legacy settings", func() {
		JustBeforeEach(func() {
			rnat.useLegacyNATSettings()
		})

		Context("and NAT is not enabled", func() {
			BeforeEach(func() {
				rnat.endpoint.Spec.NATEnabled = false
			})

			It("should select the private IP", func() {
				Expect(rnat.state).To(Equal(selectedPrivateIP))
				Expect(rnat.useIP).To(Equal(rnat.endpoint.Spec.PrivateIP))
			})
		})

		Context("and NAT is enabled", func() {
			BeforeEach(func() {
				rnat.endpoint.Spec.NATEnabled = true
			})

			It("should select the public IP", func() {
				Expect(rnat.state).To(Equal(selectedPublicIP))
				Expect(rnat.useIP).To(Equal(rnat.endpoint.Spec.PublicIP))
			})
		})

		verifyReady()
	})

	When("transition to the public IP but no check was sent", func() {
		It("should not transition the state", func() {
			oldState := rnat.state
			Expect(rnat.transitionToPublicIP(testRemoteEndpointName, false)).To(BeFalse())
			Expect(rnat.state).To(Equal(oldState))
			Expect(rnat.useIP).To(Equal(""))
		})

		verifyNotReady()
	})

	When("transition to the private IP but no check was sent", func() {
		It("should not transition the state", func() {
			oldState := rnat.state
			Expect(rnat.transitionToPrivateIP(testRemoteEndpointName, false)).To(BeFalse())
			Expect(rnat.state).To(Equal(oldState))
			Expect(rnat.useIP).To(Equal(""))
		})

		verifyNotReady()
	})

	When("transition to the private IP", func() {
		var useNAT bool
		var result bool

		JustBeforeEach(func() {
			rnat.checkSent()
			result = rnat.transitionToPrivateIP(testRemoteEndpointName, useNAT)
			rnat.checkSent()
		})

		verifyReady()

		It("should select the private IP", func() {
			Expect(result).To(BeTrue())
			Expect(rnat.state).To(Equal(selectedPrivateIP))
			Expect(rnat.useIP).To(Equal(rnat.endpoint.Spec.PrivateIP))
		})

		Context("with NAT discovered", func() {
			BeforeEach(func() {
				useNAT = true
			})

			It("should set useNAT to true", func() {
				Expect(rnat.useNAT).To(BeTrue())
			})
		})

		Context("with no NAT discovered", func() {
			BeforeEach(func() {
				useNAT = false
			})

			It("should set useNAT to false", func() {
				Expect(rnat.useNAT).To(BeFalse())
			})
		})

		Context("with no NAT discovered", func() {
			BeforeEach(func() {
				useNAT = false
			})

			It("should set useNAT to false", func() {
				Expect(rnat.useNAT).To(BeFalse())
			})
		})

		Context("after the public IP was selected", func() {
			BeforeEach(func() {
				rnat.state = selectedPublicIP
			})

			It("should not transition the state", func() {
				Expect(result).To(BeFalse())
				Expect(rnat.state).To(Equal(selectedPublicIP))
			})

			verifyNotReady()
		})
	})

	When("transition to the public IP", func() {
		var useNAT bool
		var result bool

		JustBeforeEach(func() {
			rnat.checkSent()
			result = rnat.transitionToPublicIP(testRemoteEndpointName, useNAT)
		})

		Context("with no private IP", func() {
			BeforeEach(func() {
				rnat.endpoint.Spec.PrivateIP = ""
			})

			It("should select the public IP", func() {
				Expect(result).To(BeTrue())
				Expect(rnat.state).To(Equal(selectedPublicIP))
			})

			verifyReady()
		})

		Context("with a private IP", func() {
			It("should transition to public IP pending", func() {
				Expect(result).To(BeTrue())
				Expect(rnat.state).To(Equal(selectPublicIPPending))
			})

			verifyNotReady()
		})

		It("should use the public IP", func() {
			Expect(rnat.useIP).To(Equal(rnat.endpoint.Spec.PublicIP))
		})

		Context("with NAT discovered", func() {
			BeforeEach(func() {
				useNAT = true
			})

			It("should set useNAT to true", func() {
				Expect(rnat.useNAT).To(BeTrue())
			})
		})

		Context("with no NAT discovered", func() {
			BeforeEach(func() {
				useNAT = false
			})

			It("should set useNAT to false", func() {
				Expect(rnat.useNAT).To(BeFalse())
			})
		})

		Context("after the private IP was selected", func() {
			BeforeEach(func() {
				rnat.state = selectedPrivateIP
			})

			It("should not transition the state", func() {
				Expect(result).To(BeFalse())
				Expect(rnat.state).To(Equal(selectedPrivateIP))
			})

			verifyNotReady()
		})
	})

	When("transition to the private IP after the public IP is pending", func() {
		JustBeforeEach(func() {
			rnat.checkSent()
			Expect(rnat.transitionToPublicIP(testRemoteEndpointName, true)).To(BeTrue())
		})

		Context("and the grace period has not elapsed", func() {
			JustBeforeEach(func() {
				Expect(rnat.transitionToPrivateIP(testRemoteEndpointName, false)).To(BeTrue())
			})

			It("should select the private IP", func() {
				Expect(rnat.state).To(Equal(selectedPrivateIP))
				Expect(rnat.useIP).To(Equal(rnat.endpoint.Spec.PrivateIP))
				Expect(rnat.useNAT).To(BeFalse())
			})

			verifyReady()
		})

		Context("and the grace period has elapsed", func() {
			JustBeforeEach(func() {
				rnat.lastTransition = rnat.lastTransition.Add(-time.Duration(publicToPrivateGracePeriod))
				Expect(rnat.shouldCheck()).To(BeFalse())
				Expect(rnat.transitionToPrivateIP(testRemoteEndpointName, false)).To(BeFalse())
			})

			It("should select the public IP", func() {
				Expect(rnat.state).To(Equal(selectedPublicIP))
				Expect(rnat.useIP).To(Equal(rnat.endpoint.Spec.PublicIP))
				Expect(rnat.useNAT).To(BeTrue())
			})

			verifyReady()
		})
	})
})
