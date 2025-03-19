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

package wireguard_test

import (
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cable/wireguard"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	k8snet "k8s.io/utils/net"
)

const (
	listenPort  = 123
	listenPort2 = 456
)

var _ = Describe("Driver", func() {
	Context("NewDriver", testNewDriver)
	Context("Init", testInit)
	Context("ConnectToEndpoint", testConnectToEndpoint)
	Context("DisconnectFromEndpoint", testDisconnectFromEndpoint)
	Context("GetConnections", testGetConnections)
	Context("Cleanup", testCleanup)

	Context("", func() {
		t := newTestDriver()

		Specify("GetName should return wireguard", func() {
			Expect(t.driver.GetName()).To(Equal("wireguard"))
		})
	})
})

func testNewDriver() {
	t := newTestDriver()

	BeforeEach(func() {
		t.client.devices[wireguard.DefaultDeviceName] = &wgtypes.Device{
			Peers: []wgtypes.Peer{{PublicKey: wgtypes.Key{}}},
		}

		// This should get replaced.
		_ = t.netLink.LinkAdd(&netlink.GenericLink{
			LinkAttrs: netlink.LinkAttrs{
				Name: wireguard.DefaultDeviceName,
			},
		})
	})

	It("should correctly configure the wireguard link and device", func() {
		link := t.netLink.AwaitLink(wireguard.DefaultDeviceName)
		Expect(link.Type()).To(Equal("wireguard"))

		device := t.client.devices[wireguard.DefaultDeviceName]
		Expect(device).ToNot(BeNil())
		Expect(device.PrivateKey).ToNot(Equal(wgtypes.Key{}))
		Expect(device.ListenPort).To(Equal(listenPort))
		Expect(device.Peers).To(BeEmpty())

		Expect(t.localEndpoint.Spec().BackendConfig).To(HaveKey(wireguard.PublicKey))
		Expect(t.localEndpoint.Spec().BackendConfig).To(HaveKeyWithValue(cable.InterfaceNameConfig, wireguard.DefaultDeviceName))
	})

	When("configuring the wireguard device fails", func() {
		BeforeEach(func() {
			t.client.configureDeviceErr = errors.New("mock error")
			t.checkNewDriverErr = func(err error) {
				Expect(err).To(HaveOccurred())
			}
		})

		It("should return an error", func() {
		})
	})

	When("creating the wireguard client fails", func() {
		BeforeEach(func() {
			wireguard.NewClient = func() (wireguard.Client, error) {
				return nil, errors.New("mock")
			}

			t.checkNewDriverErr = func(err error) {
				Expect(err).To(HaveOccurred())
			}
		})

		It("should return an error", func() {
		})
	})

	When("the backend port is invalid", func() {
		BeforeEach(func() {
			t.endpointSpec.BackendConfig[submarinerv1.UDPPortConfig] = "bogus"
			t.checkNewDriverErr = func(err error) {
				Expect(err).To(HaveOccurred())
			}
		})

		It("should return an error", func() {
		})
	})
}

func testInit() {
	t := newTestDriver()

	It("should succeed", func() {
		Expect(t.driver.Init()).To(Succeed())
		t.netLink.AwaitLinkSetup(wireguard.DefaultDeviceName)
	})

	When("link setup fails", func() {
		It("should return an error", func() {
			link := t.netLink.AwaitLink(wireguard.DefaultDeviceName)
			_ = t.netLink.LinkDel(link)

			Expect(t.driver.Init()).NotTo(Succeed())
		})
	})
}

func testConnectToEndpoint() {
	t := newTestDriver()

	It("should create a Connection and configure a peer on the wireguard device", func() {
		natInfo := newNATInfo("east", "20.0.0.0/16", "30.0.0.0/16")

		ip, err := t.driver.ConnectToEndpoint(natInfo)
		Expect(err).To(Succeed())
		Expect(ip).To(Equal(natInfo.UseIP))

		t.client.assertDevicePeers(natInfo)
		t.assertConnections(natInfo)

		// Calling ConnectToEndpoint again with the same endpoint should essentially be a no-op.

		ip, err = t.driver.ConnectToEndpoint(natInfo)
		Expect(err).To(Succeed())
		Expect(ip).To(Equal(natInfo.UseIP))

		t.client.assertDevicePeers(natInfo)
		t.assertConnections(natInfo)

		// Calling ConnectToEndpoint again with a differing endpoint from the same cluster should replace the previous.

		priv, err := wgtypes.GeneratePrivateKey()
		Expect(err).To(Succeed())

		natInfo.Endpoint = *natInfo.Endpoint.DeepCopy()
		natInfo.Endpoint.Spec.BackendConfig[wireguard.PublicKey] = priv.PublicKey().String()
		natInfo.Endpoint.Spec.Subnets = []string{"40.0.0.0/16"}

		ip, err = t.driver.ConnectToEndpoint(natInfo)
		Expect(err).ToNot(HaveOccurred())
		Expect(ip).To(Equal(natInfo.UseIP))

		t.client.assertDevicePeers(natInfo)
		t.assertConnections(natInfo)

		// Connect to an endpoint from a different cluster

		natInfo2 := newNATInfo("west", "50.0.0.0/16")

		ip, err = t.driver.ConnectToEndpoint(natInfo2)
		Expect(err).ToNot(HaveOccurred())
		Expect(ip).To(Equal(natInfo2.UseIP))

		t.client.assertDevicePeers(natInfo, natInfo2)
		t.assertConnections(natInfo, natInfo2)

		Expect(t.driver.GetActiveConnections()).To(BeEmpty())
	})

	When("configuring the wireguard device fails", func() {
		It("should return an error", func() {
			t.client.configureDeviceErr = errors.New("mock error")

			_, err := t.driver.ConnectToEndpoint(newNATInfo("east"))
			Expect(err).To(HaveOccurred())
		})
	})

	When("the public key is missing from the remote endpoint", func() {
		It("should return an error", func() {
			natInfo := newNATInfo("east")
			natInfo.Endpoint.Spec.BackendConfig = map[string]string{}

			_, err := t.driver.ConnectToEndpoint(natInfo)
			Expect(err).To(HaveOccurred())
		})
	})
}

func testDisconnectFromEndpoint() {
	t := newTestDriver()

	It("should remove the Connection and the wireguard device peer", func() {
		natInfo := newNATInfo("east", "20.0.0.0/16")

		_, err := t.driver.ConnectToEndpoint(natInfo)
		Expect(err).ToNot(HaveOccurred())

		natInfo2 := newNATInfo("west", "21.0.0.0/16")

		_, err = t.driver.ConnectToEndpoint(natInfo2)
		Expect(err).ToNot(HaveOccurred())

		err = t.driver.DisconnectFromEndpoint(&types.SubmarinerEndpoint{Spec: natInfo.Endpoint.Spec}, k8snet.IPv4)
		Expect(err).ToNot(HaveOccurred())

		t.client.assertDevicePeers(natInfo2)
		t.assertConnections(natInfo2)

		err = t.driver.DisconnectFromEndpoint(&types.SubmarinerEndpoint{Spec: natInfo2.Endpoint.Spec}, k8snet.IPv4)
		Expect(err).ToNot(HaveOccurred())

		t.client.assertDevicePeers()
		t.assertConnections()
	})

	When("the public key is missing from the remote endpoint", func() {
		It("should return an error", func() {
			natInfo := newNATInfo("east")
			natInfo.Endpoint.Spec.BackendConfig = map[string]string{}

			err := t.driver.DisconnectFromEndpoint(&types.SubmarinerEndpoint{Spec: natInfo.Endpoint.Spec}, k8snet.IPv4)
			Expect(err).To(HaveOccurred())
		})
	})

	When("the remote endpoint public key does not match that of the prior connection from the same cluster", func() {
		It("should return not remove the Connection", func() {
			err := t.driver.DisconnectFromEndpoint(&types.SubmarinerEndpoint{Spec: newNATInfo("east").Endpoint.Spec}, k8snet.IPv4)
			Expect(err).ToNot(HaveOccurred())

			natInfo := newNATInfo("east", "20.0.0.0/16")

			_, err = t.driver.ConnectToEndpoint(natInfo)
			Expect(err).ToNot(HaveOccurred())

			err = t.driver.DisconnectFromEndpoint(&types.SubmarinerEndpoint{Spec: newNATInfo("east").Endpoint.Spec}, k8snet.IPv4)
			Expect(err).ToNot(HaveOccurred())

			t.assertConnections(natInfo)
		})
	})
}

func testGetConnections() {
	t := newTestDriver()

	BeforeEach(func() {
		oldKeepAliveInterval := wireguard.KeepAliveInterval
		wireguard.KeepAliveInterval = time.Millisecond * 50

		oldHandshakeTimeout := wireguard.HandshakeTimeout
		wireguard.HandshakeTimeout = time.Millisecond * 100

		DeferCleanup(func() {
			wireguard.KeepAliveInterval = oldKeepAliveInterval
			wireguard.HandshakeTimeout = oldHandshakeTimeout
		})
	})

	getConnection := func() *submarinerv1.Connection {
		conns, err := t.driver.GetConnections()
		Expect(err).ToNot(HaveOccurred())
		Expect(conns).To(HaveLen(1))

		return &conns[0]
	}

	It("should correctly update the peer connection status", func() {
		_, err := t.driver.ConnectToEndpoint(newNATInfo("east", "20.0.0.0/16"))
		Expect(err).To(Succeed())

		Expect(t.client.devices[wireguard.DefaultDeviceName].Peers).To(HaveLen(1))
		peer := &t.client.devices[wireguard.DefaultDeviceName].Peers[0]

		_ = getConnection()

		By("No change - should remain Connecting")

		time.Sleep(wireguard.KeepAliveInterval + time.Millisecond*5)

		conn := getConnection()
		Expect(conn.Status).To(Equal(submarinerv1.Connecting), "Unexpected status %q", conn.StatusMessage)

		By("Initial handshake timeout - should report ConnectionError")

		time.Sleep(wireguard.HandshakeTimeout + time.Millisecond*10)

		conn = getConnection()
		Expect(conn.Status).To(Equal(submarinerv1.ConnectionError), "Unexpected status %q", conn.StatusMessage)

		By("Clear handshake timeout and add Tx bytes - should go back to Connecting since no handshake yet")

		time.Sleep(wireguard.KeepAliveInterval + time.Millisecond*5)

		wireguard.HandshakeTimeout += time.Minute
		peer.TransmitBytes = 1000
		conn = getConnection()

		Expect(conn.Status).To(Equal(submarinerv1.Connecting), "Unexpected status %q", conn.StatusMessage)

		By("Set that handshake occurred and add Rx bytes - should report Connected")

		peer.LastHandshakeTime = time.Now()
		peer.ReceiveBytes += 1000

		time.Sleep(wireguard.KeepAliveInterval + time.Millisecond*5)

		conn = getConnection()
		Expect(conn.Status).To(Equal(submarinerv1.Connected), "Unexpected status %q", conn.StatusMessage)

		By("No change - should remain Connected")

		time.Sleep(wireguard.KeepAliveInterval + time.Millisecond*5)

		conn = getConnection()
		Expect(conn.Status).To(Equal(submarinerv1.Connected), "Unexpected status %q", conn.StatusMessage)

		By("No traffic - handshake stale - should report ConnectionError")

		time.Sleep(wireguard.KeepAliveInterval + time.Millisecond*5)

		conn = getConnection()
		Expect(conn.Status).To(Equal(submarinerv1.ConnectionError), "Unexpected status %q", conn.StatusMessage)

		By("Add Tx/Rx bytes - should report Connected")

		peer.ReceiveBytes += 1000
		peer.TransmitBytes += 1000

		time.Sleep(wireguard.KeepAliveInterval + time.Millisecond*5)

		conn = getConnection()
		Expect(conn.Status).To(Equal(submarinerv1.Connected), "Unexpected status %q", conn.StatusMessage)

		By("No traffic and handshake timeout - should report ConnectionError")

		time.Sleep(wireguard.KeepAliveInterval + time.Millisecond*5)

		peer.LastHandshakeTime = time.Now().Add(-wireguard.HandshakeTimeout)
		conn = getConnection()
		Expect(conn.Status).To(Equal(submarinerv1.ConnectionError), "Unexpected status %q", conn.StatusMessage)

		By("Clear handshake timeout and add Tx/Rx bytes - should report Connected")

		peer.LastHandshakeTime = time.Now()
		peer.ReceiveBytes += 1000
		peer.TransmitBytes += 1000

		time.Sleep(wireguard.KeepAliveInterval + time.Millisecond*5)

		conn = getConnection()
		Expect(conn.Status).To(Equal(submarinerv1.Connected), "Unexpected status %q", conn.StatusMessage)
	})

	Context("with a stale peer present", func() {
		It("should remove the stale peer", func() {
			key, err := wgtypes.GenerateKey()
			Expect(err).ToNot(HaveOccurred())

			t.client.devices[wireguard.DefaultDeviceName].Peers = append(t.client.devices[wireguard.DefaultDeviceName].Peers,
				wgtypes.Peer{PublicKey: key})

			_, err = t.driver.GetConnections()
			Expect(err).ToNot(HaveOccurred())

			Expect(t.client.devices[wireguard.DefaultDeviceName].Peers).To(BeEmpty())
		})
	})

	When("device retrieval fails", func() {
		It("should return an error", func() {
			t.client.devices = map[string]*wgtypes.Device{}
			_, err := t.driver.GetConnections()
			Expect(err).To(HaveOccurred())
		})
	})
}

func testCleanup() {
	t := newTestDriver()

	It("should remove the wireguard link", func() {
		Expect(t.driver.Cleanup()).To(Succeed())
		t.netLink.AwaitNoLink(wireguard.DefaultDeviceName)
	})
}
