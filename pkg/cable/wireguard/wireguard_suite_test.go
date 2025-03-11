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
	"flag"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	"github.com/submariner-io/admiral/pkg/resource"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cable/wireguard"
	"github.com/submariner-io/submariner/pkg/endpoint"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	fakeNetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	"github.com/submariner-io/submariner/pkg/types"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	k8snet "k8s.io/utils/net"
	"k8s.io/utils/ptr"
)

func init() {
	kzerolog.AddFlags(nil)
}

var _ = BeforeSuite(func() {
	flags := flag.NewFlagSet("kzerolog", flag.ExitOnError)
	kzerolog.AddFlags(flags)
	_ = flags.Parse([]string{"-v=4"})

	kzerolog.InitK8sLogging()
})

func TestWireguard(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Wireguard Suite")
}

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

		wireguard.NewClient = func() (wireguard.Client, error) {
			return t.client, nil
		}

		t.checkNewDriverErr = func(err error) {
			Expect(err).NotTo(HaveOccurred())
		}
	})

	JustBeforeEach(func() {
		var err error

		t.localEndpoint = endpoint.NewLocal(&t.endpointSpec, dynamicfake.NewSimpleDynamicClient(scheme.Scheme), "")

		t.driver, err = wireguard.NewDriver(t.localEndpoint, &types.SubmarinerCluster{})
		t.checkNewDriverErr(err)
	})

	return t
}

func (t *testDriver) assertConnections(natInfos ...*natdiscovery.NATEndpointInfo) {
	actual, err := t.driver.GetConnections()
	Expect(err).ToNot(HaveOccurred())

	for i := range actual {
		actual[i].StatusMessage = ""
	}

	slices.SortFunc(actual, func(a, b submarinerv1.Connection) int {
		return strings.Compare(a.Endpoint.BackendConfig[wireguard.PublicKey], b.Endpoint.BackendConfig[wireguard.PublicKey])
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
		return strings.Compare(a.Endpoint.BackendConfig[wireguard.PublicKey], b.Endpoint.BackendConfig[wireguard.PublicKey])
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
					wireguard.PublicKey:        priv.PublicKey().String(),
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
				PublicKey:    pc.PublicKey,
				PresharedKey: ptr.Deref(pc.PresharedKey, wgtypes.Key{}),
				Endpoint:     pc.Endpoint,
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
	device, err := c.Device(wireguard.DefaultDeviceName)
	Expect(err).ToNot(HaveOccurred())

	for i := range natInfos {
		index := slices.IndexFunc(device.Peers, func(p wgtypes.Peer) bool {
			return p.PublicKey.String() == natInfos[i].Endpoint.Spec.BackendConfig[wireguard.PublicKey]
		})
		Expect(index).To(BeNumerically(">=", 0), "Missing expected device peer for %s", resource.ToJSON(natInfos[i]))

		peer := &device.Peers[index]
		Expect(peer.PublicKey.String()).To(Equal(natInfos[i].Endpoint.Spec.BackendConfig[wireguard.PublicKey]))
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
