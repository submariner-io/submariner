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
	"errors"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submendpoint "github.com/submariner-io/submariner/pkg/endpoint"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	natproto "github.com/submariner-io/submariner/pkg/natdiscovery/proto"
	"google.golang.org/protobuf/proto"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
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

func init() {
	kzerolog.AddFlags(nil)
	utilruntime.Must(submarinerv1.AddToScheme(scheme.Scheme))
}

var _ = BeforeSuite(func() {
	kzerolog.InitK8sLogging()
})

var (
	testLocalNATPort  int32 = 1234
	testRemoteNATPort int32 = 4321
)

type UDPReadInfo struct {
	b    []byte
	addr *net.UDPAddr
}

//nolint:gocritic // Ignore "exposedSyncMutex: don't embed sync.Mutex"
type FakeServerConnection struct {
	sync.Mutex
	addr           *net.UDPAddr
	udpSentChannel chan []byte
	udpReadChannel chan UDPReadInfo
	closed         atomic.Bool
}

func (c *FakeServerConnection) Close() error {
	c.Lock()
	defer c.Unlock()

	if c.closed.CompareAndSwap(false, true) {
		close(c.udpReadChannel)
	}

	return nil
}

func (c *FakeServerConnection) ReadFromUDP(b []byte) (int, *net.UDPAddr, error) {
	i, ok := <-c.udpReadChannel
	if !ok {
		return 0, nil, nil
	}

	copy(b, i.b)

	return len(i.b), i.addr, nil
}

func (c *FakeServerConnection) WriteToUDP(b []byte, _ *net.UDPAddr) (int, error) {
	c.udpSentChannel <- b
	return len(b), nil
}

func (c *FakeServerConnection) awaitSent() []byte {
	select {
	case b := <-c.udpSentChannel:
		return b
	case <-time.After(3 * time.Second):
		Fail("Nothing received from the channel")
		return nil
	}
}

func (c *FakeServerConnection) forwardTo(to *FakeServerConnection, howMany int) {
	if howMany == 0 {
		return
	}

	count := 0

	go func() {
		for b := range c.udpSentChannel {
			to.inputFrom(b, c.addr)

			count++
			if howMany > 0 && count >= howMany {
				break
			}
		}
	}()
}

func (c *FakeServerConnection) inputFrom(b []byte, addr *net.UDPAddr) {
	c.Lock()
	defer c.Unlock()

	c.udpReadChannel <- UDPReadInfo{
		b:    b,
		addr: addr,
	}
}

type NATDiscoveryInfo struct {
	localEndpoint  *submendpoint.Local
	instance       natdiscovery.Interface
	ipv4Connection *FakeServerConnection
	checkDiscovery func()
}

func newNATDiscovery(endpoint *submarinerv1.Endpoint, addr *net.UDPAddr) *NATDiscoveryInfo {
	dynClient := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	localEndpoint := submendpoint.NewLocal(&endpoint.Spec, dynClient, "")

	test.CreateResource(dynClient.Resource(submarinerv1.EndpointGVR).Namespace(""), localEndpoint.Resource())

	natDiscoveryInfo := &NATDiscoveryInfo{
		localEndpoint: localEndpoint,
		ipv4Connection: &FakeServerConnection{
			udpSentChannel: make(chan []byte, 10),
			udpReadChannel: make(chan UDPReadInfo, 10),
			addr:           addr,
		},
	}

	srcIP := addr.IP.String()

	var err error

	natDiscoveryInfo.instance, err = natdiscovery.NewWithConfig(natdiscovery.Config{
		LocalEndpoint: localEndpoint,
		CreateServerConnection: func(port int32, family k8snet.IPFamily) (natdiscovery.ServerConnection, error) {
			defer GinkgoRecover()
			Expect(family).ToNot(Equal(k8snet.IPFamilyUnknown))
			Expect(int(port)).To(Equal(addr.Port))

			if family == k8snet.IPv4 {
				return natDiscoveryInfo.ipv4Connection, nil
			}

			return nil, errors.New("unsupported IP family")
		},
		FindSourceIP: func(_ string, family k8snet.IPFamily) string {
			defer GinkgoRecover()
			Expect(family).ToNot(Equal(k8snet.IPFamilyUnknown))

			if family == k8snet.IPv4 {
				return srcIP
			}

			return ""
		},
		RunLoop: func(_ <-chan struct{}, doCheck func()) {
			natDiscoveryInfo.checkDiscovery = doCheck
		},
	})
	Expect(err).To(Succeed())

	stopCh := make(chan struct{})

	DeferCleanup(func() {
		close(stopCh)
		Eventually(func() bool {
			return natDiscoveryInfo.ipv4Connection.closed.Load()
		}).Should(BeTrue())
	})

	Expect(natDiscoveryInfo.instance.Run(stopCh)).To(Succeed())

	return natDiscoveryInfo
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

func parseProtocolRequest(buf []byte) *natproto.SubmarinerNATDiscoveryRequest {
	msg := natproto.SubmarinerNATDiscoveryMessage{}
	Expect(proto.Unmarshal(buf, &msg)).To(Succeed())

	request := msg.GetRequest()
	Expect(request).NotTo(BeNil())

	return request
}

func parseProtocolResponse(buf []byte) *natproto.SubmarinerNATDiscoveryResponse {
	msg := natproto.SubmarinerNATDiscoveryMessage{}
	Expect(proto.Unmarshal(buf, &msg)).To(Succeed())

	response := msg.GetResponse()
	Expect(response).NotTo(BeNil())

	return response
}

func TestNATDiscovery(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "NAT Discovery Suite")
}
