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
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("remoteEndpointNAT", func() {

	var rnat *remoteEndpointNAT

	BeforeEach(func() {
		remoteEndpoint := createTestRemoteEndpoint()
		rnat = newRemoteEndpointNAT(&remoteEndpoint)
	})

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
		Context("and NAT is not enabled", func() {
			It("should select the private IP", func() {
				rnat.endpoint.Spec.NATEnabled = false
				rnat.useLegacyNATSettings()
				Expect(rnat.state).To(Equal(selectedPrivateIP))
				Expect(rnat.useIP).To(Equal(rnat.endpoint.Spec.PrivateIP))
			})
		})
		Context("and NAT is enabled", func() {
			It("should select the public IP", func() {
				rnat.endpoint.Spec.NATEnabled = true
				rnat.useLegacyNATSettings()
				Expect(rnat.state).To(Equal(selectedPublicIP))
				Expect(rnat.useIP).To(Equal(rnat.endpoint.Spec.PublicIP))
			})
		})
	})

	When("the public IP is selected but no check was sent", func() {
		It("it should not transition the state", func() {
			oldState := rnat.state
			Expect(rnat.transitionToPublicIP(testRemoteEndpointName, false)).To(BeFalse())
			Expect(rnat.state).To(Equal(oldState))
			Expect(rnat.useIP).To(Equal(""))
		})
	})

	When("the private IP is selected but no check was sent", func() {
		It("it should not transition the state", func() {
			oldState := rnat.state
			Expect(rnat.transitionToPrivateIP(testRemoteEndpointName, false)).To(BeFalse())
			Expect(rnat.state).To(Equal(oldState))
			Expect(rnat.useIP).To(Equal(""))
		})
	})

	When("the private IP is selected", func() {
		var useNAT bool

		JustBeforeEach(func() {
			rnat.checkSent()
			Expect(rnat.transitionToPrivateIP(testRemoteEndpointName, useNAT)).To(BeTrue())
			Expect(rnat.state).To(Equal(selectedPrivateIP))
		})

		It("should use the private IP", func() {
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
	})

	When("the public IP is selected", func() {
		var useNAT bool

		JustBeforeEach(func() {
			rnat.checkSent()
			Expect(rnat.transitionToPublicIP(testRemoteEndpointName, useNAT)).To(BeTrue())
			Expect(rnat.state).To(Equal(selectedPublicIP))
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
	})

	When("private IP is selected right after publicIP ", func() {
		Context("and the grace period has not elapsed", func() {
			It("should use the private IP", func() {
				rnat.checkSent()
				Expect(rnat.transitionToPublicIP(testRemoteEndpointName, true)).To(BeTrue())
				Expect(rnat.transitionToPrivateIP(testRemoteEndpointName, false)).To(BeTrue())
				Expect(rnat.state).To(Equal(selectedPrivateIP))
				Expect(rnat.useIP).To(Equal(rnat.endpoint.Spec.PrivateIP))
				Expect(rnat.useNAT).To(BeFalse())
			})
		})

		Context("and the grace period has elapsed", func() {
			It("should still use the public IP", func() {
				rnat.checkSent()
				Expect(rnat.transitionToPublicIP(testRemoteEndpointName, true)).To(BeTrue())
				rnat.lastTransition = rnat.lastTransition.Add(-time.Duration(publicToPrivateFailoverTimeout))
				Expect(rnat.transitionToPrivateIP(testRemoteEndpointName, false)).To(BeFalse())
				Expect(rnat.state).To(Equal(selectedPublicIP))
				Expect(rnat.useIP).To(Equal(rnat.endpoint.Spec.PublicIP))
				Expect(rnat.useNAT).To(BeTrue())
			})
		})
	})
})
