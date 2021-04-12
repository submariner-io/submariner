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
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	natproto "github.com/submariner-io/submariner/pkg/natdiscovery/proto"
)

var _ = When("a request is sent", func() {
	var (
		request        *natproto.SubmarinerNatDiscoveryRequest
		remoteEndpoint submarinerv1.Endpoint
		udpSent        chan []byte
		ndInstance     *natDiscovery
	)

	localEndpoint := createTestLocalEndpoint()

	BeforeEach(func() {
		remoteEndpoint = createTestRemoteEndpoint()
		remoteEndpoint.Spec.PublicIP = ""
		remoteEndpoint.Spec.PrivateIP = ""
	})

	JustBeforeEach(func() {
		ndInstance, udpSent, _ = createTestListener(&localEndpoint)
		ndInstance.findSrcIP = func(_ string) string { return testLocalPrivateIP }

		err := ndInstance.sendCheckRequest(newRemoteEndpointNAT(&remoteEndpoint))
		Expect(err).NotTo(HaveOccurred())

		request = parseProtocolRequest(awaitChan(udpSent))
	})

	testRequest := func(srcIP string) {
		It("should set the sender fields correctly", func() {
			Expect(request.Sender).NotTo(BeNil())
			Expect(request.Sender.ClusterId).To(Equal(testLocalClusterID))
			Expect(request.Sender.EndpointId).To(Equal(testLocalEndpointName))
		})

		It("should set the receiver fields correctly", func() {
			Expect(request.Receiver).NotTo(BeNil())
			Expect(request.Receiver.ClusterId).To(Equal(testRemoteClusterID))
			Expect(request.Receiver.EndpointId).To(Equal(testRemoteEndpointName))
		})

		It("should set the using source fields correctly", func() {
			Expect(request.UsingSrc).NotTo(BeNil())
			Expect(request.UsingSrc.IP).To(Equal(testLocalPrivateIP))
			Expect(request.UsingSrc.Port).To(Equal(uint32(testLocalNATPort)))
		})

		It("should set the using destination fields correctly", func() {
			Expect(request.UsingDst).NotTo(BeNil())
			Expect(request.UsingDst.Port).To(Equal(uint32(testRemoteNATPort)))
			Expect(request.UsingDst.IP).To(Equal(srcIP))
		})

		It("should not send another request", func() {
			Consistently(udpSent).ShouldNot(Receive())
		})
	}

	Context("with the private IP set", func() {
		BeforeEach(func() {
			remoteEndpoint.Spec.PrivateIP = testRemotePrivateIP
		})

		testRequest(testRemotePrivateIP)

		Context("and the public IP set", func() {
			BeforeEach(func() {
				remoteEndpoint.Spec.PublicIP = testRemotePublicIP
			})

			JustBeforeEach(func() {
				request = parseProtocolRequest(awaitChan(udpSent))
			})

			testRequest(testRemotePublicIP)
		})
	})

	Context("with the public IP set", func() {
		BeforeEach(func() {
			remoteEndpoint.Spec.PublicIP = testRemotePublicIP
		})

		testRequest(testRemotePublicIP)
	})
})
