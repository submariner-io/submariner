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

	natproto "github.com/submariner-io/submariner/pkg/natdiscovery/proto"
)

var _ = Describe("natdiscovery requests", func() {

	var ndInstance *natDiscovery
	var udpSent chan []byte

	localEndpoint := createTestLocalEndpoint()
	remoteEndpoint := createTestRemoteEndpoint()

	BeforeEach(func() {
		ndInstance, udpSent = createTestListener(&localEndpoint)
		ndInstance.AddEndpoint(&remoteEndpoint)
	})

	makeRequestToRemote := func() *natproto.SubmarinerNatDiscoveryRequest {
		err := ndInstance.sendCheckRequestByRemoteID(testRemoteEndpointName)
		Expect(err).NotTo(HaveOccurred())
		return parseProtocolRequest(<-udpSent)
	}

	When("sending a request", func() {
		It("should fill up the sender fields correctly", func() {
			request := makeRequestToRemote()
			Expect(request.Sender).NotTo(BeNil())
			Expect(request.Sender.ClusterId).To(Equal(testLocalClusterID))
			Expect(request.Sender.EndpointId).To(Equal(testLocalEndpointName))
		})

		It("should fill up the receiver fields correctly", func() {
			request := makeRequestToRemote()
			Expect(request.Receiver).NotTo(BeNil())
			Expect(request.Receiver.ClusterId).To(Equal(testRemoteClusterID))
			Expect(request.Receiver.EndpointId).To(Equal(testRemoteEndpointName))
		})

		It("should fill up the using-src fields correctly", func() {
			request := makeRequestToRemote()
			Expect(request.UsingSrc).NotTo(BeNil())
			Expect(request.UsingSrc.Port).To(Equal(uint32(testLocalNATPort)))
		})

		It("should fill up the using-dst fields correctly", func() {
			request := makeRequestToRemote()
			Expect(request.UsingDst).NotTo(BeNil())
			Expect(request.UsingDst.Port).To(Equal(uint32(testRemoteNATPort)))
			Expect(request.UsingDst.IP).To(Equal(testRemotePrivateIP))
		})

	})
})
