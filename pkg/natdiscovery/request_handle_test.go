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
	"google.golang.org/protobuf/proto"
	k8snet "k8s.io/utils/net"
)

var _ = Describe("Request handling", func() {
	var (
		localND        *NATDiscoveryInfo
		remoteND       *NATDiscoveryInfo
		localEndpoint  submarinerv1.Endpoint
		remoteEndpoint submarinerv1.Endpoint
	)

	BeforeEach(func() {
		localEndpoint = createTestLocalEndpoint()
		remoteEndpoint = createTestRemoteEndpoint()

		localND = newNATDiscovery(&localEndpoint, &net.UDPAddr{
			IP:   net.ParseIP(testLocalPrivateIP),
			Port: int(testLocalNATPort),
		})

		remoteND = newNATDiscovery(&remoteEndpoint, &net.UDPAddr{
			IP:   net.ParseIP(testRemotePrivateIP),
			Port: int(testRemoteNATPort),
		})
	})

	parseResponseInLocalListener := func(udpPacket []byte, remoteAddr *net.UDPAddr) *natproto.SubmarinerNATDiscoveryResponse {
		localND.ipv4Connection.inputFrom(udpPacket, remoteAddr)
		return parseProtocolResponse(localND.ipv4Connection.awaitSent())
	}

	requestResponseFromRemoteToLocal := func(remoteAddr *net.UDPAddr) []*natproto.SubmarinerNATDiscoveryResponse {
		remoteND.instance.AddEndpoint(&localEndpoint, k8snet.IPv4)
		remoteND.checkDiscovery()

		return []*natproto.SubmarinerNATDiscoveryResponse{
			parseResponseInLocalListener(remoteND.ipv4Connection.awaitSent(), remoteAddr), /* Private IP request */
			parseResponseInLocalListener(remoteND.ipv4Connection.awaitSent(), remoteAddr), /* Public IP request */
		}
	}

	When("receiving a request with a known sender endpoint", func() {
		It("should respond with OK", func() {
			localND.instance.AddEndpoint(&remoteEndpoint, k8snet.IPv4)
			response := requestResponseFromRemoteToLocal(remoteND.ipv4Connection.addr)
			Expect(response[0].GetResponse()).To(Equal(natproto.ResponseType_OK))
			Expect(response[1].GetResponse()).To(Equal(natproto.ResponseType_NAT_DETECTED))
			Expect(response[1].GetDstIpNatDetected()).To(BeTrue())
			Expect(response[1].GetSrcIpNatDetected()).To(BeFalse())
			Expect(response[1].GetSrcPortNatDetected()).To(BeFalse())
		})

		Context("with a modified IP", func() {
			It("should respond with NAT_DETECTED and SrcIpNatDetected", func() {
				remoteND.ipv4Connection.addr.IP = net.ParseIP(testRemotePublicIP)
				localND.instance.AddEndpoint(&remoteEndpoint, k8snet.IPv4)
				response := requestResponseFromRemoteToLocal(remoteND.ipv4Connection.addr)
				Expect(response[0].GetResponse()).To(Equal(natproto.ResponseType_NAT_DETECTED))
				Expect(response[0].GetSrcIpNatDetected()).To(BeTrue())
				Expect(response[0].GetSrcPortNatDetected()).To(BeFalse())
			})
		})

		Context("with a modified port", func() {
			It("should respond with NAT_DETECTED and SrcPortNatDetected", func() {
				remoteND.ipv4Connection.addr.Port = int(testRemoteNATPort + 1)
				localND.instance.AddEndpoint(&remoteEndpoint, k8snet.IPv4)
				response := requestResponseFromRemoteToLocal(remoteND.ipv4Connection.addr)
				Expect(response[0].GetResponse()).To(Equal(natproto.ResponseType_NAT_DETECTED))
				Expect(response[0].GetSrcIpNatDetected()).To(BeFalse())
				Expect(response[0].GetSrcPortNatDetected()).To(BeTrue())
			})
		})
	})

	When("receiving a request with an unknown receiver endpoint ID", func() {
		It("should respond with UNKNOWN_DST_ENDPOINT", func() {
			localND.instance.AddEndpoint(&remoteEndpoint, k8snet.IPv4)
			localEndpoint.Spec.CableName = "invalid"
			response := requestResponseFromRemoteToLocal(remoteND.ipv4Connection.addr)
			Expect(response[0].GetResponse()).To(Equal(natproto.ResponseType_UNKNOWN_DST_ENDPOINT))
		})
	})

	When("receiving a request with an unknown receiver cluster ID", func() {
		It("should respond with UNKNOWN_DST_CLUSTER", func() {
			localND.instance.AddEndpoint(&remoteEndpoint, k8snet.IPv4)
			localEndpoint.Spec.ClusterID = "invalid"
			response := requestResponseFromRemoteToLocal(remoteND.ipv4Connection.addr)
			Expect(response[0].GetResponse()).To(Equal(natproto.ResponseType_UNKNOWN_DST_CLUSTER))
		})
	})

	When("receiving a request with a missing Sender", func() {
		It("should respond with MALFORMED", func() {
			request := createMalformedRequest(func(msg *natproto.SubmarinerNATDiscoveryMessage) {
				msg.GetRequest().Sender = nil
			})
			response := parseResponseInLocalListener(request, remoteND.ipv4Connection.addr)
			Expect(response.GetResponse()).To(Equal(natproto.ResponseType_MALFORMED))
		})
	})

	When("receiving a malformed request with a missing Receiver", func() {
		It("should respond with MALFORMED", func() {
			request := createMalformedRequest(func(msg *natproto.SubmarinerNATDiscoveryMessage) {
				msg.GetRequest().Receiver = nil
			})
			response := parseResponseInLocalListener(request, remoteND.ipv4Connection.addr)
			Expect(response.GetResponse()).To(Equal(natproto.ResponseType_MALFORMED))
		})
	})

	When("receiving a malformed request with a missing UsingDst", func() {
		It("should respond with MALFORMED", func() {
			request := createMalformedRequest(func(msg *natproto.SubmarinerNATDiscoveryMessage) {
				msg.GetRequest().UsingDst = nil
			})
			response := parseResponseInLocalListener(request, remoteND.ipv4Connection.addr)
			Expect(response.GetResponse()).To(Equal(natproto.ResponseType_MALFORMED))
		})
	})

	When("receiving a malformed request with a missing UsingSrc", func() {
		It("should respond with MALFORMED", func() {
			request := createMalformedRequest(func(msg *natproto.SubmarinerNATDiscoveryMessage) {
				msg.GetRequest().UsingSrc = nil
			})
			response := parseResponseInLocalListener(request, remoteND.ipv4Connection.addr)
			Expect(response.GetResponse()).To(Equal(natproto.ResponseType_MALFORMED))
		})
	})
})

func createMalformedRequest(mangleFunction func(*natproto.SubmarinerNATDiscoveryMessage)) []byte {
	request := natproto.SubmarinerNATDiscoveryRequest{
		RequestNumber: 1,
		Sender: &natproto.EndpointDetails{
			EndpointId: testRemoteEndpointNameAndFamily,
			ClusterId:  testRemoteClusterID,
		},
		Receiver: &natproto.EndpointDetails{
			EndpointId: testLocalEndpointNameAndFamily,
			ClusterId:  testLocalClusterID,
		},
		UsingSrc: &natproto.IPPortPair{
			IP:   testRemotePrivateIP,
			Port: natproto.DefaultPort,
		},
		UsingDst: &natproto.IPPortPair{
			IP:   testLocalPrivateIP,
			Port: natproto.DefaultPort,
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
