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

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	natproto "github.com/submariner-io/submariner/pkg/natdiscovery/proto"
	"google.golang.org/protobuf/proto"
)

var _ = Describe("Request handling", func() {

	var localListener *natDiscovery
	var localUDPSent chan []byte
	var remoteListener *natDiscovery
	var remoteUDPSent chan []byte
	var localEndpoint submarinerv1.Endpoint
	var remoteEndpoint submarinerv1.Endpoint

	var remoteUDPAddr net.UDPAddr

	BeforeEach(func() {
		localEndpoint = createTestLocalEndpoint()
		remoteEndpoint = createTestRemoteEndpoint()

		localListener, localUDPSent, _ = createTestListener(&localEndpoint)
		localListener.findSrcIP = func(_ string) string { return testLocalPrivateIP }
		remoteListener, remoteUDPSent, _ = createTestListener(&remoteEndpoint)
		remoteListener.findSrcIP = func(_ string) string { return testRemotePrivateIP }

		remoteUDPAddr = net.UDPAddr{
			IP:   net.ParseIP(testRemotePrivateIP),
			Port: int(testRemoteNATPort),
		}
	})

	parseResponseInLocalListener := func(udpPacket []byte, remoteAddr *net.UDPAddr) *natproto.SubmarinerNATDiscoveryResponse {
		err := localListener.parseAndHandleMessageFromAddress(udpPacket, remoteAddr)
		Expect(err).NotTo(HaveOccurred())
		return parseProtocolResponse(awaitChan(localUDPSent))
	}

	requestResponseFromRemoteToLocal := func(remoteAddr *net.UDPAddr) []*natproto.SubmarinerNATDiscoveryResponse {
		err := remoteListener.sendCheckRequest(newRemoteEndpointNAT(&localEndpoint))
		Expect(err).NotTo(HaveOccurred())
		return []*natproto.SubmarinerNATDiscoveryResponse{
			parseResponseInLocalListener(awaitChan(remoteUDPSent), remoteAddr), /* Private IP request */
			parseResponseInLocalListener(awaitChan(remoteUDPSent), remoteAddr), /* Public IP request */
		}
	}

	When("receiving a request with a known sender endpoint", func() {
		It("should respond with OK", func() {
			localListener.AddEndpoint(&remoteEndpoint)
			response := requestResponseFromRemoteToLocal(&remoteUDPAddr)
			Expect(response[0].Response).To(Equal(natproto.ResponseType_OK))
			Expect(response[1].Response).To(Equal(natproto.ResponseType_NAT_DETECTED))
			Expect(response[1].DstIpNatDetected).To(BeTrue())
			Expect(response[1].SrcIpNatDetected).To(BeFalse())
			Expect(response[1].SrcPortNatDetected).To(BeFalse())

		})

		Context("with a modified IP", func() {
			It("should respond with NAT_DETECTED and SrcIpNatDetected", func() {
				remoteUDPAddr.IP = net.ParseIP(testRemotePublicIP)
				localListener.AddEndpoint(&remoteEndpoint)
				response := requestResponseFromRemoteToLocal(&remoteUDPAddr)
				Expect(response[0].Response).To(Equal(natproto.ResponseType_NAT_DETECTED))
				Expect(response[0].SrcIpNatDetected).To(BeTrue())
				Expect(response[0].SrcPortNatDetected).To(BeFalse())
			})
		})

		Context("with a modified port", func() {
			It("should respond with NAT_DETECTED and SrcPortNatDetected", func() {
				remoteUDPAddr.Port = int(testRemoteNATPort + 1)
				localListener.AddEndpoint(&remoteEndpoint)
				response := requestResponseFromRemoteToLocal(&remoteUDPAddr)
				Expect(response[0].Response).To(Equal(natproto.ResponseType_NAT_DETECTED))
				Expect(response[0].SrcIpNatDetected).To(BeFalse())
				Expect(response[0].SrcPortNatDetected).To(BeTrue())
			})
		})
	})

	When("receiving a request with an unknown receiver endpoint ID", func() {
		It("should respond with UNKNOWN_DST_ENDPOINT", func() {
			localListener.AddEndpoint(&remoteEndpoint)
			localEndpoint.Spec.CableName = "invalid"
			response := requestResponseFromRemoteToLocal(&remoteUDPAddr)
			Expect(response[0].Response).To(Equal(natproto.ResponseType_UNKNOWN_DST_ENDPOINT))
		})
	})

	When("receiving a request with an unknown receiver cluster ID", func() {
		It("should respond with UNKNOWN_DST_CLUSTER", func() {
			localListener.AddEndpoint(&remoteEndpoint)
			localEndpoint.Spec.ClusterID = "invalid"
			response := requestResponseFromRemoteToLocal(&remoteUDPAddr)
			Expect(response[0].Response).To(Equal(natproto.ResponseType_UNKNOWN_DST_CLUSTER))
		})
	})

	When("receiving a request with a missing Sender", func() {
		It("should respond with MALFORMED", func() {
			request := createMalformedRequest(func(msg *natproto.SubmarinerNATDiscoveryMessage) {
				msg.GetRequest().Sender = nil
			})
			response := parseResponseInLocalListener(request, &remoteUDPAddr)
			Expect(response.Response).To(Equal(natproto.ResponseType_MALFORMED))
		})
	})

	When("receiving a malformed request with a missing Receiver", func() {
		It("should respond with MALFORMED", func() {
			request := createMalformedRequest(func(msg *natproto.SubmarinerNATDiscoveryMessage) {
				msg.GetRequest().Receiver = nil
			})
			response := parseResponseInLocalListener(request, &remoteUDPAddr)
			Expect(response.Response).To(Equal(natproto.ResponseType_MALFORMED))
		})
	})

	When("receiving a malformed request with a missing UsingDst", func() {
		It("should respond with MALFORMED", func() {
			request := createMalformedRequest(func(msg *natproto.SubmarinerNATDiscoveryMessage) {
				msg.GetRequest().UsingDst = nil
			})
			response := parseResponseInLocalListener(request, &remoteUDPAddr)
			Expect(response.Response).To(Equal(natproto.ResponseType_MALFORMED))
		})
	})

	When("receiving a malformed request with a missing UsingSrc", func() {
		It("should respond with MALFORMED", func() {
			request := createMalformedRequest(func(msg *natproto.SubmarinerNATDiscoveryMessage) {
				msg.GetRequest().UsingSrc = nil
			})
			response := parseResponseInLocalListener(request, &remoteUDPAddr)
			Expect(response.Response).To(Equal(natproto.ResponseType_MALFORMED))
		})
	})
})

func createMalformedRequest(mangleFunction func(*natproto.SubmarinerNATDiscoveryMessage)) []byte {
	request := natproto.SubmarinerNATDiscoveryRequest{
		RequestNumber: 1,
		Sender: &natproto.EndpointDetails{
			EndpointId: testRemoteEndpointName,
			ClusterId:  testRemoteClusterID,
		},
		Receiver: &natproto.EndpointDetails{
			EndpointId: testLocalEndpointName,
			ClusterId:  testLocalClusterID,
		},
		UsingSrc: &natproto.IPPortPair{
			IP:   testRemotePrivateIP,
			Port: uint32(natproto.DefaultPort),
		},
		UsingDst: &natproto.IPPortPair{
			IP:   testLocalPrivateIP,
			Port: uint32(natproto.DefaultPort),
		},
	}

	msgRequest := &natproto.SubmarinerNATDiscoveryMessage_Request{
		Request: &request,
	}

	message := natproto.SubmarinerNATDiscoveryMessage{
		Version: natproto.Version,
		Message: msgRequest,
	}

	mangleFunction(&message)

	buf, err := proto.Marshal(&message)
	Expect(err).NotTo(HaveOccurred())

	return buf
}
