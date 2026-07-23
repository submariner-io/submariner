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

package amneziawg_test

import (
	"context"
	"errors"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/advanced-wg/awgctrl-go/wgtypes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/resource"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cable/amneziawg"
	"github.com/submariner-io/submariner/pkg/endpoint"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	fakeNetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/vishvananda/netlink"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	k8snet "k8s.io/utils/net"
	"k8s.io/utils/ptr"
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

		Specify("GetName should return amneziawg", func() {
			Expect(t.driver.GetName()).To(Equal("amneziawg"))
		})
	})
})

type noopUserspaceDevice struct{}

func (noopUserspaceDevice) Close() error { return nil }

type testDriver struct {
	endpointSpec      submarinerv1.EndpointSpec
	localEndpoint     *endpoint.Local
	driver            cable.Driver
	netLink           *fakeNetlink.NetLink
	client            *fakeClient
	checkNewDriverErr func(error)
}

func newTestDriver() *testDriver {
	t := &testDriver{}

	BeforeEach(func() {
		os.Setenv("CE_IPSEC_PSK", "test-psk-value")
		os.Unsetenv(cable.CableDriverOptionsEnv)

		t.endpointSpec = submarinerv1.EndpointSpec{
			ClusterID:  "local",
			CableName:  "submariner-cable-local-192-68-1-1",
			PrivateIPs: []string{"192.68.1.1"},
			Subnets:    []string{"10.0.0.0/16"},
			BackendConfig: map[string]string{
				submarinerv1.UDPPortConfig: strconv.Itoa(listenPort),
			},
		}

		t.netLink = fakeNetlink.New()
		netlinkAPI.NewFunc = func() netlinkAPI.Interface {
			return t.netLink
		}

		t.client = &fakeClient{}
		t.client.devices = map[string]*wgtypes.Device{}

		amneziawg.NewClient = func() (amneziawg.Client, error) {
			return t.client, nil
		}

		amneziawg.StartUserspaceDevice = func(ifaceName string) (amneziawg.UserspaceDevice, error) {
			_ = t.netLink.LinkAdd(&netlink.GenericLink{
				LinkAttrs: netlink.LinkAttrs{
					Name: ifaceName,
				},
			})

			return noopUserspaceDevice{}, nil
		}

		t.checkNewDriverErr = func(err error) {
			Expect(err).NotTo(HaveOccurred())
		}
	})

	JustBeforeEach(func() {
		var err error

		t.localEndpoint = endpoint.NewLocal(&t.endpointSpec, dynamicfake.NewSimpleDynamicClient(scheme.Scheme), "")

		t.driver, err = amneziawg.NewDriver(t.localEndpoint, &types.SubmarinerCluster{}, nil)
		t.checkNewDriverErr(err)
	})

	return t
}

func testNewDriver() {
	t := newTestDriver()

	BeforeEach(func() {
		t.client.devices[amneziawg.DefaultDeviceName] = &wgtypes.Device{
			Peers: []wgtypes.Peer{{PublicKey: wgtypes.Key{}}},
		}
	})

	It("should configure the device with obfuscation parameters and public key", func() {
		device := t.client.devices[amneziawg.DefaultDeviceName]
		Expect(device).ToNot(BeNil())
		Expect(device.ListenPort).To(Equal(listenPort))
		Expect(device.PublicKey).ToNot(Equal(wgtypes.Key{}))
		Expect(device.PrivateKey).ToNot(Equal(wgtypes.Key{}))
		Expect(device.Jc).To(Equal(7))
		Expect(device.Jmin).To(Equal(80))
		Expect(device.Jmax).To(Equal(200))
		Expect(device.S1).To(Equal(45))
		Expect(device.S2).To(Equal(60))
		Expect(device.S3).To(Equal(35))
		Expect(device.S4).To(Equal(12))
		Expect(device.H1).To(Equal("200000000-280000000"))
		Expect(device.H2).To(Equal("400000000-480000000"))
		Expect(device.I1).To(Equal("<r 40>"))
		Expect(device.Peers).To(BeEmpty(), "ReplacePeers should clear the pre-seeded peer")

		Expect(t.localEndpoint.Spec().BackendConfig[amneziawg.PublicKey]).To(Equal(device.PublicKey.String()))
		Expect(t.localEndpoint.Spec().BackendConfig[cable.InterfaceNameConfig]).To(Equal(amneziawg.DefaultDeviceName))
	})

	When("cable driver options are set", func() {
		BeforeEach(func() {
			os.Setenv(cable.CableDriverOptionsEnv, `{"jc":"3","h1":"10-20","i1":"<b 0x64>"}`)
		})

		AfterEach(func() {
			os.Unsetenv(cable.CableDriverOptionsEnv)
		})

		It("should override the default obfuscation parameters", func() {
			device := t.client.devices[amneziawg.DefaultDeviceName]
			Expect(device.Jc).To(Equal(3))
			Expect(device.H1).To(Equal("10-20"))
			Expect(device.I1).To(Equal("<b 0x64>"))
			Expect(device.S3).To(Equal(35))
		})
	})

	When("cable driver options are invalid", func() {
		BeforeEach(func() {
			os.Setenv(cable.CableDriverOptionsEnv, `{"jc":"0"}`)

			t.checkNewDriverErr = func(err error) {
				Expect(err).To(HaveOccurred())
			}
		})

		AfterEach(func() {
			os.Unsetenv(cable.CableDriverOptionsEnv)
		})

		It("should return an error", func() {
		})
	})

	When("configuring the AmneziaWG device fails", func() {
		BeforeEach(func() {
			t.client.configureDeviceErr = errors.New("mock error")
			t.checkNewDriverErr = func(err error) {
				Expect(err).To(HaveOccurred())
			}
		})

		It("should return an error", func() {
		})
	})

	When("creating the AmneziaWG client fails", func() {
		BeforeEach(func() {
			amneziawg.NewClient = func() (amneziawg.Client, error) {
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
		Expect(t.driver.Init(context.TODO())).To(Succeed())
		t.netLink.AwaitLinkSetup(amneziawg.DefaultDeviceName)
	})

	When("link setup fails", func() {
		It("should return an error", func() {
			link := t.netLink.AwaitLink(amneziawg.DefaultDeviceName)
			_ = t.netLink.LinkDel(link)

			Expect(t.driver.Init(context.TODO())).NotTo(Succeed())
		})
	})
}

func testConnectToEndpoint() {
	t := newTestDriver()

	When("keepalive cable driver option is set", func() {
		BeforeEach(func() {
			os.Setenv(cable.CableDriverOptionsEnv, `{"keepalive":"25"}`)
		})

		AfterEach(func() {
			os.Unsetenv(cable.CableDriverOptionsEnv)
		})

		It("should use the configured keepalive when connecting", func() {
			natInfo := newNATInfo("remote", "172.16.0.0/16")
			_, err := t.driver.ConnectToEndpoint(natInfo)
			Expect(err).NotTo(HaveOccurred())

			device, err := t.client.Device(amneziawg.DefaultDeviceName)
			Expect(err).NotTo(HaveOccurred())
			Expect(device.Peers).To(HaveLen(1))
			Expect(device.Peers[0].PersistentKeepaliveInterval).To(Equal(25 * time.Second))
		})
	})

	It("should create a Connection and configure a peer on the AmneziaWG device", func() {
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
		natInfo.Endpoint.Spec.BackendConfig[amneziawg.PublicKey] = priv.PublicKey().String()
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

	When("configuring the AmneziaWG device fails", func() {
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

	It("should remove the Connection and the AmneziaWG device peer", func() {
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
		// Disable peer keepalive so the no-traffic grace window also uses StatusPollInterval
		// (otherwise default keepalive=10s would delay soft ConnectionError in this suite).
		os.Setenv(cable.CableDriverOptionsEnv, `{"keepalive":"0"}`)

		oldStatusPollInterval := amneziawg.StatusPollInterval
		amneziawg.StatusPollInterval = time.Millisecond * 50

		oldHandshakeTimeout := amneziawg.HandshakeTimeout
		amneziawg.HandshakeTimeout = time.Millisecond * 100

		DeferCleanup(func() {
			os.Unsetenv(cable.CableDriverOptionsEnv)

			amneziawg.StatusPollInterval = oldStatusPollInterval
			amneziawg.HandshakeTimeout = oldHandshakeTimeout
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

		Expect(t.client.devices[amneziawg.DefaultDeviceName].Peers).To(HaveLen(1))
		peer := &t.client.devices[amneziawg.DefaultDeviceName].Peers[0]

		_ = getConnection()

		By("No change - should remain Connecting")

		time.Sleep(amneziawg.StatusPollInterval + time.Millisecond*5)

		conn := getConnection()
		Expect(conn.Status).To(Equal(submarinerv1.Connecting), "Unexpected status %q", conn.StatusMessage)

		By("Initial handshake timeout - should report ConnectionError")

		time.Sleep(amneziawg.HandshakeTimeout + time.Millisecond*10)

		conn = getConnection()
		Expect(conn.Status).To(Equal(submarinerv1.ConnectionError), "Unexpected status %q", conn.StatusMessage)

		By("Clear handshake timeout and add Tx bytes - should go back to Connecting since no handshake yet")

		time.Sleep(amneziawg.StatusPollInterval + time.Millisecond*5)

		amneziawg.HandshakeTimeout += time.Minute
		peer.TransmitBytes = 1000
		conn = getConnection()

		Expect(conn.Status).To(Equal(submarinerv1.Connecting), "Unexpected status %q", conn.StatusMessage)

		By("Set that handshake occurred and add Rx bytes - should report Connected")

		peer.LastHandshakeTime = time.Now()
		peer.ReceiveBytes += 1000

		time.Sleep(amneziawg.StatusPollInterval + time.Millisecond*5)

		conn = getConnection()
		Expect(conn.Status).To(Equal(submarinerv1.Connected), "Unexpected status %q", conn.StatusMessage)

		By("No change - should remain Connected")

		time.Sleep(amneziawg.StatusPollInterval + time.Millisecond*5)

		conn = getConnection()
		Expect(conn.Status).To(Equal(submarinerv1.Connected), "Unexpected status %q", conn.StatusMessage)

		By("No traffic - handshake stale - should report ConnectionError")

		time.Sleep(amneziawg.StatusPollInterval + time.Millisecond*5)

		conn = getConnection()
		Expect(conn.Status).To(Equal(submarinerv1.ConnectionError), "Unexpected status %q", conn.StatusMessage)

		By("Add Tx/Rx bytes - should report Connected")

		peer.ReceiveBytes += 1000
		peer.TransmitBytes += 1000

		time.Sleep(amneziawg.StatusPollInterval + time.Millisecond*5)

		conn = getConnection()
		Expect(conn.Status).To(Equal(submarinerv1.Connected), "Unexpected status %q", conn.StatusMessage)

		By("No traffic and handshake timeout - should report ConnectionError")

		time.Sleep(amneziawg.StatusPollInterval + time.Millisecond*5)

		peer.LastHandshakeTime = time.Now().Add(-amneziawg.HandshakeTimeout)
		conn = getConnection()
		Expect(conn.Status).To(Equal(submarinerv1.ConnectionError), "Unexpected status %q", conn.StatusMessage)

		By("Clear handshake timeout and add Tx/Rx bytes - should report Connected")

		peer.LastHandshakeTime = time.Now()
		peer.ReceiveBytes += 1000
		peer.TransmitBytes += 1000

		time.Sleep(amneziawg.StatusPollInterval + time.Millisecond*5)

		conn = getConnection()
		Expect(conn.Status).To(Equal(submarinerv1.Connected), "Unexpected status %q", conn.StatusMessage)
	})

	Context("with a stale peer present", func() {
		It("should remove the stale peer", func() {
			key, err := wgtypes.GenerateKey()
			Expect(err).ToNot(HaveOccurred())

			t.client.devices[amneziawg.DefaultDeviceName].Peers = append(t.client.devices[amneziawg.DefaultDeviceName].Peers,
				wgtypes.Peer{PublicKey: key})

			_, err = t.driver.GetConnections()
			Expect(err).ToNot(HaveOccurred())

			Expect(t.client.devices[amneziawg.DefaultDeviceName].Peers).To(BeEmpty())
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

	It("should delete the device link", func() {
		Expect(t.driver.Cleanup(context.TODO())).To(Succeed())
		t.netLink.AwaitNoLink(amneziawg.DefaultDeviceName)
	})
}

func (t *testDriver) assertConnections(natInfos ...*natdiscovery.NATEndpointInfo) {
	actual, err := t.driver.GetConnections()
	Expect(err).ToNot(HaveOccurred())

	for i := range actual {
		actual[i].StatusMessage = ""
	}

	slices.SortFunc(actual, func(a, b submarinerv1.Connection) int {
		return strings.Compare(a.Endpoint.BackendConfig[amneziawg.PublicKey], b.Endpoint.BackendConfig[amneziawg.PublicKey])
	})

	expected := make([]submarinerv1.Connection, len(natInfos))
	for i := range natInfos {
		expected[i] = submarinerv1.Connection{
			Status:   submarinerv1.Connecting,
			Endpoint: natInfos[i].Endpoint.Spec,
			UsingIP:  natInfos[i].UseIP,
			UsingNAT: natInfos[i].UseNAT,
		}
	}

	slices.SortFunc(expected, func(a, b submarinerv1.Connection) int {
		return strings.Compare(a.Endpoint.BackendConfig[amneziawg.PublicKey], b.Endpoint.BackendConfig[amneziawg.PublicKey])
	})

	Expect(actual).To(HaveExactElements(expected))
}

func newNATInfo(clusterID string, subnets ...string) *natdiscovery.NATEndpointInfo {
	priv, err := wgtypes.GeneratePrivateKey()
	Expect(err).ToNot(HaveOccurred())

	return &natdiscovery.NATEndpointInfo{
		Endpoint: submarinerv1.Endpoint{
			Spec: submarinerv1.EndpointSpec{
				ClusterID: clusterID,
				CableName: "submariner-cable-" + clusterID,
				Subnets:   subnets,
				BackendConfig: map[string]string{
					amneziawg.PublicKey:        priv.PublicKey().String(),
					submarinerv1.UDPPortConfig: strconv.Itoa(listenPort2),
				},
			},
		},
		UseIP:     "172.93.2.1",
		UseNAT:    true,
		UseFamily: k8snet.IPv4,
	}
}

type fakeClient struct {
	devices            map[string]*wgtypes.Device
	configureDeviceErr error
}

//nolint:gocritic // hugeParam: matches amneziawg.Client / awgctrl API
func (c *fakeClient) ConfigureDevice(name string, cfg wgtypes.Config) error {
	if c.configureDeviceErr != nil {
		return c.configureDeviceErr
	}

	d := c.devices[name]
	if d == nil {
		c.devices[name] = &wgtypes.Device{}
		d = c.devices[name]
	}

	if cfg.PrivateKey != nil {
		d.PrivateKey = *cfg.PrivateKey
		d.PublicKey = d.PrivateKey.PublicKey()
	}

	if cfg.ListenPort != nil {
		d.ListenPort = *cfg.ListenPort
	}

	applyIntOpt := func(dst *int, src *int) {
		if src != nil {
			*dst = *src
		}
	}

	applyStrOpt := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}

	applyIntOpt(&d.Jc, cfg.Jc)
	applyIntOpt(&d.Jmin, cfg.Jmin)
	applyIntOpt(&d.Jmax, cfg.Jmax)
	applyIntOpt(&d.S1, cfg.S1)
	applyIntOpt(&d.S2, cfg.S2)
	applyIntOpt(&d.S3, cfg.S3)
	applyIntOpt(&d.S4, cfg.S4)
	applyStrOpt(&d.H1, cfg.H1)
	applyStrOpt(&d.H2, cfg.H2)
	applyStrOpt(&d.H3, cfg.H3)
	applyStrOpt(&d.H4, cfg.H4)
	applyStrOpt(&d.I1, cfg.I1)
	applyStrOpt(&d.I2, cfg.I2)
	applyStrOpt(&d.I3, cfg.I3)
	applyStrOpt(&d.I4, cfg.I4)
	applyStrOpt(&d.I5, cfg.I5)

	if cfg.ReplacePeers {
		d.Peers = nil
	}

	for i := range cfg.Peers {
		pc := &cfg.Peers[i]
		if pc.Remove {
			d.Peers = slices.DeleteFunc(d.Peers, func(p wgtypes.Peer) bool {
				return p.PublicKey.String() == pc.PublicKey.String()
			})

			continue
		}

		index := slices.IndexFunc(d.Peers, func(p wgtypes.Peer) bool {
			return p.PublicKey.String() == pc.PublicKey.String()
		})

		if index == -1 {
			if pc.UpdateOnly {
				continue
			}

			d.Peers = append(d.Peers, wgtypes.Peer{
				PublicKey:                   pc.PublicKey,
				PresharedKey:                ptr.Deref(pc.PresharedKey, wgtypes.Key{}),
				Endpoint:                    pc.Endpoint,
				PersistentKeepaliveInterval: ptr.Deref(pc.PersistentKeepaliveInterval, 0),
			})

			index = len(d.Peers) - 1
		}

		peer := &d.Peers[index]

		if pc.ReplaceAllowedIPs {
			peer.AllowedIPs = pc.AllowedIPs
		} else {
			peer.AllowedIPs = append(peer.AllowedIPs, pc.AllowedIPs...)
		}
	}

	return nil
}

func (c *fakeClient) Device(name string) (*wgtypes.Device, error) {
	if c.devices[name] != nil {
		d := *c.devices[name]
		d.Peers = make([]wgtypes.Peer, len(c.devices[name].Peers))
		copy(d.Peers, c.devices[name].Peers)

		return &d, nil
	}

	return nil, os.ErrNotExist
}

func (c *fakeClient) Close() error {
	return nil
}

func (c *fakeClient) assertDevicePeers(natInfos ...*natdiscovery.NATEndpointInfo) {
	device, err := c.Device(amneziawg.DefaultDeviceName)
	Expect(err).ToNot(HaveOccurred())

	for i := range natInfos {
		index := slices.IndexFunc(device.Peers, func(p wgtypes.Peer) bool {
			return p.PublicKey.String() == natInfos[i].Endpoint.Spec.BackendConfig[amneziawg.PublicKey]
		})
		Expect(index).To(BeNumerically(">=", 0), "Missing expected device peer for %s", resource.ToJSON(natInfos[i]))

		peer := &device.Peers[index]
		Expect(peer.PublicKey.String()).To(Equal(natInfos[i].Endpoint.Spec.BackendConfig[amneziawg.PublicKey]))
		Expect(peer.PresharedKey).ToNot(Equal(wgtypes.Key{}))
		Expect(peer.Endpoint).To(Equal(&net.UDPAddr{
			IP:   net.ParseIP(natInfos[i].UseIP),
			Port: listenPort2,
		}))

		actualIPs := make([]string, len(peer.AllowedIPs))
		for j := range peer.AllowedIPs {
			actualIPs[j] = peer.AllowedIPs[j].String()
		}

		slices.Sort(actualIPs)
		slices.Sort(natInfos[i].Endpoint.Spec.Subnets)
		Expect(actualIPs).To(HaveExactElements(natInfos[i].Endpoint.Spec.Subnets))

		device.Peers = slices.Delete(device.Peers, index, index+1)
	}

	Expect(device.Peers).To(BeEmpty(), "Received unexpected device peers")
}
