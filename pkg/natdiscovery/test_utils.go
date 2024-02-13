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
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submendpoint "github.com/submariner-io/submariner/pkg/endpoint"
	natproto "github.com/submariner-io/submariner/pkg/natdiscovery/proto"
	"google.golang.org/protobuf/proto"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
)

const (
	testLocalEndpointName = "cluster-a-ep-1"
	testLocalClusterID    = "cluster-a"
	testLocalPublicIP     = "10.1.1.1"
	testLocalPrivateIP    = "2.2.2.2"

	testRemoteEndpointName = "cluster-b-ep-1"
	testRemoteClusterID    = "cluster-b"
	testRemotePublicIP     = "10.3.3.3"
	testRemotePrivateIP    = "4.4.4.4"
	testRemotePrivateIP2   = "5.5.5.5"
)

var (
	testLocalNATPort  int32 = 1234
	testRemoteNATPort int32 = 4321
)

func parseProtocolRequest(buf []byte) *natproto.SubmarinerNATDiscoveryRequest {
	msg := natproto.SubmarinerNATDiscoveryMessage{}
	if err := proto.Unmarshal(buf, &msg); err != nil {
		logger.Errorf(err, "error unmarshaling message received on UDP port %d", natproto.DefaultPort)
	}

	request := msg.GetRequest()
	Expect(request).NotTo(BeNil())

	return request
}

func parseProtocolResponse(buf []byte) *natproto.SubmarinerNATDiscoveryResponse {
	msg := natproto.SubmarinerNATDiscoveryMessage{}
	if err := proto.Unmarshal(buf, &msg); err != nil {
		logger.Errorf(err, "error unmarshaling message received on UDP port %d", natproto.DefaultPort)
	}

	response := msg.GetResponse()
	Expect(response).NotTo(BeNil())

	return response
}

func createTestListener(endpoint *submarinerv1.Endpoint) (*natDiscovery, chan []byte, chan *NATEndpointInfo) {
	dynClient := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	localEndpoint := submendpoint.NewLocal(&endpoint.Spec, dynClient, "")

	test.CreateResource(dynClient.Resource(submarinerv1.EndpointGVR).Namespace(""), localEndpoint.Resource())

	listener, err := newNATDiscovery(localEndpoint)
	Expect(err).To(Succeed())

	readyChannel := listener.GetReadyChannel()

	udpSentChannel := make(chan []byte, 10)
	listener.serverUDPWrite = func(b []byte, _ *net.UDPAddr) (int, error) {
		udpSentChannel <- b
		return len(b), nil
	}

	return listener, udpSentChannel, readyChannel
}

func forwardFromUDPChan(from chan []byte, addr *net.UDPAddr, to *natDiscovery, howMany int) {
	if howMany == 0 {
		return
	}

	count := 0

	go func() {
		for p := range from {
			err := to.parseAndHandleMessageFromAddress(p, addr)
			if err != nil {
				logger.Errorf(err, "Error handling message")
			}

			count++
			if howMany > 0 && count >= howMany {
				break
			}
		}
	}()
}

func createTestLocalEndpoint() submarinerv1.Endpoint {
	return submarinerv1.Endpoint{
		Spec: submarinerv1.EndpointSpec{
			CableName:  testLocalEndpointName,
			ClusterID:  testLocalClusterID,
			PublicIP:   testLocalPublicIP,
			PrivateIP:  testLocalPrivateIP,
			NATEnabled: true,
			BackendConfig: map[string]string{
				submarinerv1.NATTDiscoveryPortConfig: strconv.Itoa(int(testLocalNATPort)),
			},
		},
	}
}

func createTestRemoteEndpoint() submarinerv1.Endpoint {
	return submarinerv1.Endpoint{
		Spec: submarinerv1.EndpointSpec{
			CableName:  testRemoteEndpointName,
			ClusterID:  testRemoteClusterID,
			PublicIP:   testRemotePublicIP,
			PrivateIP:  testRemotePrivateIP,
			NATEnabled: true,
			BackendConfig: map[string]string{
				submarinerv1.NATTDiscoveryPortConfig: strconv.Itoa(int(testRemoteNATPort)),
			},
		},
	}
}

func awaitChan(from chan []byte) []byte {
	select {
	case res := <-from:
		return res
	case <-time.After(3 * time.Second):
		Fail("Nothing received from the channel")
		return nil
	}
}
