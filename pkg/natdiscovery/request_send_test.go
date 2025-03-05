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
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	natproto "github.com/submariner-io/submariner/pkg/natdiscovery/proto"
	k8snet "k8s.io/utils/net"
)

var _ = When("a request is sent", func() {
	var (
		request          *natproto.SubmarinerNATDiscoveryRequest
		remoteEndpoint   submarinerv1.Endpoint
		serverConnection *FakeServerConnection
	)

	localEndpoint := createTestLocalEndpoint()

	BeforeEach(func() {
		remoteEndpoint = createTestRemoteEndpoint()
		remoteEndpoint.Spec.PublicIPs = []string{}
		remoteEndpoint.Spec.PrivateIPs = []string{}
	})

	JustBeforeEach(func() {
		nd := newNATDiscovery(&localEndpoint, &net.UDPAddr{
			IP:   net.ParseIP(testLocalPrivateIP),
			Port: int(testLocalNATPort),
		})

		nd.instance.AddEndpoint(&remoteEndpoint, k8snet.IPv4)
		nd.checkDiscovery()

		serverConnection = nd.ipv4Connection

		request = parseProtocolRequest(serverConnection.awaitSent())
	})

	testRequest := func(srcIP string) {
		It("should set the sender fields correctly", func() {
			Expect(request.GetSender()).NotTo(BeNil())
			Expect(request.GetSender().GetClusterId()).To(Equal(testLocalClusterID))
			Expect(request.GetSender().GetEndpointId()).To(Equal(testLocalEndpointNameAndFamily))
		})

		It("should set the receiver fields correctly", func() {
			Expect(request.GetReceiver()).NotTo(BeNil())
			Expect(request.GetReceiver().GetClusterId()).To(Equal(testRemoteClusterID))
			Expect(request.GetReceiver().GetEndpointId()).To(Equal(testRemoteEndpointNameAndFamily))
		})

		It("should set the using source fields correctly", func() {
			Expect(request.GetUsingSrc()).NotTo(BeNil())
			Expect(request.GetUsingSrc().GetIP()).To(Equal(testLocalPrivateIP))
			Expect(request.GetUsingSrc().GetPort()).To(Equal(testLocalNATPort))
		})

		It("should set the using destination fields correctly", func() {
			Expect(request.GetUsingDst()).NotTo(BeNil())
			Expect(request.GetUsingDst().GetPort()).To(Equal(testRemoteNATPort))
			Expect(request.GetUsingDst().GetIP()).To(Equal(srcIP))
		})

		It("should not send another request", func() {
			Consistently(serverConnection.udpSentChannel).ShouldNot(Receive())
		})
	}

	Context("with the private IP set", func() {
		BeforeEach(func() {
			remoteEndpoint.Spec.PrivateIPs = []string{testRemotePrivateIP}
		})

		testRequest(testRemotePrivateIP)

		Context("and the public IP set", func() {
			BeforeEach(func() {
				remoteEndpoint.Spec.PublicIPs = []string{testRemotePublicIP}
			})

			JustBeforeEach(func() {
				request = parseProtocolRequest(serverConnection.awaitSent())
			})

			testRequest(testRemotePublicIP)
		})
	})

	Context("with the public IP set", func() {
		BeforeEach(func() {
			remoteEndpoint.Spec.PublicIPs = []string{testRemotePublicIP}
		})

		testRequest(testRemotePublicIP)
	})
})
