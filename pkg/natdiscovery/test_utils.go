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
	"sync/atomic"
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
	k8snet "k8s.io/utils/net"
)

const (
	testLocalEndpointName = "cluster-a-ep-1"
	// endpointId as formated in GetFamilyCableName.
	testLocalEndpointNameAndFamily = testLocalEndpointName + "-v" + string(k8snet.IPv4)
	testLocalClusterID             = "cluster-a"
	testLocalPublicIP              = "10.1.1.1"
	testLocalPrivateIP             = "2.2.2.2"

	testRemoteEndpointName          = "cluster-b-ep-1"
	testRemoteEndpointNameAndFamily = "cluster-b-ep-1" + "-v" + string(k8snet.IPv4)
	testRemoteClusterID             = "cluster-b"
	testRemotePublicIP              = "10.3.3.3"
	testRemotePrivateIP             = "4.4.4.4"
	testRemotePrivateIP2            = "5.5.5.5"
)

var (
	testLocalNATPort  int32 = 1234
	testRemoteNATPort int32 = 4321
)

type UDPReadInfo struct {
	b    []byte
	addr *net.UDPAddr
}

type fakeServerConnection struct {
	addr           *net.UDPAddr
	udpSentChannel chan []byte
	udpReadChannel chan UDPReadInfo
	closed         atomic.Bool
}

func (c *fakeServerConnection) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		close(c.udpReadChannel)
	}

	return nil
}

func (c *fakeServerConnection) ReadFromUDP(b []byte) (int, *net.UDPAddr, error) {
	i, ok := <-c.udpReadChannel
	if !ok {
		return 0, nil, nil
	}

	copy(b, i.b)

	return len(i.b), i.addr, nil
}

func (c *fakeServerConnection) WriteToUDP(b []byte, _ *net.UDPAddr) (int, error) {
	c.udpSentChannel <- b
	return len(b), nil
}

func (c *fakeServerConnection) awaitSent() []byte {
	select {
	case res := <-c.udpSentChannel:
		return res
	case <-time.After(3 * time.Second):
		Fail("Nothing received from the channel")
		return nil
	}
}

func (c *fakeServerConnection) forwardFromSent(to *fakeServerConnection, howMany int) {
	if howMany == 0 {
		return
	}

	count := 0

	go func() {
		for b := range c.udpSentChannel {
			to.input(b, c.addr)

			count++
			if howMany > 0 && count >= howMany {
				break
			}
		}
	}()
}

func (c *fakeServerConnection) input(b []byte, addr *net.UDPAddr) {
	c.udpReadChannel <- UDPReadInfo{
		b:    b,
		addr: addr,
	}
}

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

func createTestListener(endpoint *submarinerv1.Endpoint, addr *net.UDPAddr) (*natDiscovery, *fakeServerConnection, chan *NATEndpointInfo) {
	dynClient := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	localEndpoint := submendpoint.NewLocal(&endpoint.Spec, dynClient, "")

	test.CreateResource(dynClient.Resource(submarinerv1.EndpointGVR).Namespace(""), localEndpoint.Resource())

	listener, err := newNATDiscovery(localEndpoint)
	Expect(err).To(Succeed())

	readyChannel := listener.GetReadyChannel()

	serverConnection := &fakeServerConnection{
		udpSentChannel: make(chan []byte, 10),
		udpReadChannel: make(chan UDPReadInfo, 10),
		addr:           addr,
	}

	listener.createServerConnection = func(_ int32, _ k8snet.IPFamily) (ServerConnection, error) {
		return serverConnection, nil
	}

	stopCh := make(chan struct{})

	DeferCleanup(func() {
		close(stopCh)
		Eventually(func() bool {
			return serverConnection.closed.Load()
		}).Should(BeTrue())
	})

	Expect(listener.runListeners(stopCh)).To(Succeed())

	return listener, serverConnection, readyChannel
}

func createTestLocalEndpoint() submarinerv1.Endpoint {
	return submarinerv1.Endpoint{
		Spec: submarinerv1.EndpointSpec{
			CableName:  testLocalEndpointName,
			ClusterID:  testLocalClusterID,
			PublicIPs:  []string{testLocalPublicIP},
			PrivateIPs: []string{testLocalPrivateIP},
			Subnets:    []string{"10.0.0.0/16"},
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
			PublicIPs:  []string{testRemotePublicIP},
			PrivateIPs: []string{testRemotePrivateIP},
			Subnets:    []string{"11.0.0.0/16"},
			NATEnabled: true,
			BackendConfig: map[string]string{
				submarinerv1.NATTDiscoveryPortConfig: strconv.Itoa(int(testRemoteNATPort)),
			},
		},
	}
}
