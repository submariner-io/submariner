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
	"net"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	natproto "github.com/submariner-io/submariner/pkg/natdiscovery/proto"
)

var _ = Describe("natdiscovery listener", func() {

	var localListener *natDiscovery
	var localUDPSent chan []byte
	var remoteListener *natDiscovery
	var remoteUDPSent chan []byte

	localEndpoint := createTestLocalEndpoint()
	remoteEndpoint := createTestRemoteEndpoint()
	remoteUnknownEndpoint := createTestUnknownRemoteEndpoint()

	remoteUDPAddr := net.UDPAddr{
		IP:   net.ParseIP(testRemotePrivateIP),
		Port: int(testRemoteNATPort),
	}

	BeforeEach(func() {
		localListener, localUDPSent = createTestListener(&localEndpoint)
		localListener.findSrcIP = func(_ string) (string, error) { return testLocalPrivateIP, nil }
		remoteListener, remoteUDPSent = createTestListener(&remoteEndpoint)
		remoteListener.findSrcIP = func(_ string) (string, error) { return testRemotePrivateIP, nil }
	})

	parseResponseInLocalListener := func(udpPacket []byte, remoteAddr *net.UDPAddr) *natproto.SubmarinerNatDiscoveryResponse {
		err := localListener.parseAndHandleMessageFromAddress(udpPacket, remoteAddr)
		Expect(err).NotTo(HaveOccurred())
		return parseProtocolResponse(<-localUDPSent)
	}
	requestResponseFromRemoteToLocal := func(remoteAddr *net.UDPAddr) []*natproto.SubmarinerNatDiscoveryResponse {
		err := remoteListener.sendCheckRequestByRemoteID(testLocalEndpointName)
		Expect(err).NotTo(HaveOccurred())
		return []*natproto.SubmarinerNatDiscoveryResponse{
			parseResponseInLocalListener(<-remoteUDPSent, remoteAddr), /* Private IP request */
			parseResponseInLocalListener(<-remoteUDPSent, remoteAddr), /* Public IP request */
		}
	}

	When("receiving a request from an unknown endpoint on a known cluster", func() {
		It("it should respond unknown endpoint", func() {
			remoteListener.AddEndpoint(&localEndpoint)
			localListener.AddEndpoint(&remoteUnknownEndpoint)
			response := requestResponseFromRemoteToLocal(&remoteUDPAddr)
			Expect(response[0].Response).To(Equal(natproto.ResponseType_UNKNOWN_SRC_ENDPOINT))
		})
	})

	When("receiving a request from an known cluster", func() {
		It("it should respond OK", func() {
			remoteListener.AddEndpoint(&localEndpoint)
			localListener.AddEndpoint(&remoteEndpoint)
			response := requestResponseFromRemoteToLocal(&remoteUDPAddr)
			Expect(response[0].Response).To(Equal(natproto.ResponseType_OK))
		})
	})

	When("receiving a request from an known cluster, but IP is modified", func() {
		It("it should respond OK", func() {
			remoteUDPAddrOtherIP := net.UDPAddr{
				IP:   net.ParseIP(testRemotePublicIP),
				Port: int(testRemoteNATPort),
			}
			remoteListener.AddEndpoint(&localEndpoint)
			localListener.AddEndpoint(&remoteEndpoint)
			response := requestResponseFromRemoteToLocal(&remoteUDPAddrOtherIP)
			Expect(response[0].Response).To(Equal(natproto.ResponseType_SRC_MODIFIED))
		})
	})

	When("receiving a request from an known cluster, but port is modified", func() {
		It("it should respond OK", func() {
			remoteUDPAddrOtherPort := net.UDPAddr{
				IP:   net.ParseIP(testRemotePrivateIP),
				Port: int(testRemoteNATPort + 1),
			}
			remoteListener.AddEndpoint(&localEndpoint)
			localListener.AddEndpoint(&remoteEndpoint)
			response := requestResponseFromRemoteToLocal(&remoteUDPAddrOtherPort)
			Expect(response[0].Response).To(Equal(natproto.ResponseType_SRC_MODIFIED))
		})
	})

	When("receiving a malformed request", func() {
		It("it should respond MALFORMED for a missing Sender", func() {
			request := createMalformedRequest(func(msg *natproto.SubmarinerNatDiscoveryMessage) {
				msg.GetRequest().Sender = nil
			})
			response := parseResponseInLocalListener(request, &remoteUDPAddr)
			Expect(response.Response).To(Equal(natproto.ResponseType_MALFORMED))
		})
	})

	When("receiving a malformed request", func() {
		It("it should respond MALFORMED for a missing Receiver", func() {
			request := createMalformedRequest(func(msg *natproto.SubmarinerNatDiscoveryMessage) {
				msg.GetRequest().Receiver = nil
			})
			response := parseResponseInLocalListener(request, &remoteUDPAddr)
			Expect(response.Response).To(Equal(natproto.ResponseType_MALFORMED))
		})
	})

	When("receiving a malformed request", func() {
		It("it should respond MALFORMED for a missing UsingDst", func() {
			request := createMalformedRequest(func(msg *natproto.SubmarinerNatDiscoveryMessage) {
				msg.GetRequest().UsingDst = nil
			})
			response := parseResponseInLocalListener(request, &remoteUDPAddr)
			Expect(response.Response).To(Equal(natproto.ResponseType_MALFORMED))
		})
	})

	When("receiving a malformed request", func() {
		It("it should respond MALFORMED for a missing UsingSrc", func() {
			request := createMalformedRequest(func(msg *natproto.SubmarinerNatDiscoveryMessage) {
				msg.GetRequest().UsingSrc = nil
			})
			response := parseResponseInLocalListener(request, &remoteUDPAddr)
			Expect(response.Response).To(Equal(natproto.ResponseType_MALFORMED))
		})
	})
})

func createMalformedRequest(mangleFunction func(*natproto.SubmarinerNatDiscoveryMessage)) []byte {
	request := natproto.SubmarinerNatDiscoveryRequest{
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

	msgRequest := &natproto.SubmarinerNatDiscoveryMessage_Request{
		Request: &request,
	}

	message := natproto.SubmarinerNatDiscoveryMessage{
		Version: natproto.Version,
		Message: msgRequest,
	}

	mangleFunction(&message)

	buf, err := proto.Marshal(&message)
	Expect(err).NotTo(HaveOccurred())

	return buf
}
