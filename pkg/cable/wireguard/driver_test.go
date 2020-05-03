package wireguard

import (
	"net"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

var _ = BeforeSuite(func() {
	wgDeviceCreator = mockDeviceCreator{}
})

var _ = Describe("WireGuard Driver Tests", func() {
	const localClusterID = "localClusterID"
	var driver *wireguard
	var spec subv1.EndpointSpec
	var pubKey wgtypes.Key
	var privateIP string

	It("should create WG driver", func() {
		lep := types.SubmarinerEndpoint{Spec: subv1.EndpointSpec{ClusterID: localClusterID, BackendConfig: map[string]string{}}}
		w, err := NewDriver([]string{"10.1.0.0/16", "10.0.0.0/16"}, lep)
		Ω(err).ShouldNot(HaveOccurred())
		Ω(lep.Spec.BackendConfig[PublicKey]).ShouldNot(Equal(""), "PublicKey is nil ")
		driver = w.(*wireguard)
	})
	It("create remote endpoint spec with WG keys", func() {
		spec = subv1.EndpointSpec{ClusterID: "anotherCluster", BackendConfig: map[string]string{}}
		prKey, err := wgtypes.GeneratePrivateKey()
		Ω(err).ShouldNot(HaveOccurred())
		pubKey = prKey.PublicKey()
		spec.BackendConfig[PublicKey] = pubKey.String()
		spec.Subnets = []string{"10.4.0.0/16", "10.3.0.0/16"}
		privateIP = "10.3.0.3"
		spec.PrivateIP = privateIP
		// we will use it in the next tests
		tc := driver.client.(*testClient)
		tc.DeviceFunc = func(str string) (*wgtypes.Device, error) {
			return &wgtypes.Device{Peers: []wgtypes.Peer{wgtypes.Peer{PublicKey: pubKey}}}, nil
		}
	})
	When("check ConnectToEndpoint", func() {
		It("connect to itself", func() {
			ip, err := driver.ConnectToEndpoint(types.SubmarinerEndpoint{Spec: subv1.EndpointSpec{ClusterID: localClusterID}})
			Ω(err).ShouldNot(HaveOccurred())
			Ω(ip).Should(Equal(""))
		})
		It("reconnectToEndpoint, with the same key ", func() {
			driver.peers[spec.ClusterID] = pubKey
			ip, err := driver.ConnectToEndpoint(types.SubmarinerEndpoint{Spec: spec})
			Ω(err).ShouldNot(HaveOccurred())
			Ω(ip).Should(Equal(privateIP))
			Ω(driver.peers[spec.ClusterID]).Should(Equal(pubKey))
		})
		It("reconnectToEndpoint, with a new key ", func() {
			priveK, err := wgtypes.GeneratePrivateKey()
			Ω(err).ShouldNot(HaveOccurred())
			pubK := priveK.PublicKey()
			driver.peers[spec.ClusterID] = pubK
			ip, err := driver.ConnectToEndpoint(types.SubmarinerEndpoint{Spec: spec})
			Ω(err).ShouldNot(HaveOccurred())
			Ω(ip).Should(Equal(privateIP))
			Ω(driver.peers[spec.ClusterID]).Should(Equal(pubKey))
		})
	})
	When("check DisconnectFromEndpoint", func() {
		It("disconnect from itself", func() {
			Ω(driver.DisconnectFromEndpoint(types.SubmarinerEndpoint{Spec: subv1.EndpointSpec{ClusterID: localClusterID}})).ShouldNot(HaveOccurred())
		})
		It("DisconnectFromEndpoint, with the old key ", func() {
			priveK, err := wgtypes.GeneratePrivateKey()
			Ω(err).ShouldNot(HaveOccurred())
			pubK := priveK.PublicKey()
			driver.peers[spec.ClusterID] = pubK
			Ω(driver.DisconnectFromEndpoint(types.SubmarinerEndpoint{Spec: spec})).ShouldNot(HaveOccurred())
			Ω(driver.peers[spec.ClusterID]).ShouldNot(Equal(pubKey))
			Ω(driver.peers[spec.ClusterID]).Should(Equal(pubK))
		})
		It("DisconnectFromEndpoint, with the same key ", func() {
			driver.peers[spec.ClusterID] = pubKey
			Ω(driver.DisconnectFromEndpoint(types.SubmarinerEndpoint{Spec: spec})).ShouldNot(HaveOccurred())
			Ω(driver.peers[spec.ClusterID]).ShouldNot(Equal(pubKey))
		})
	})
})

type mockDeviceCreator struct{}

func (m mockDeviceCreator) createWGClient() (wgClient, error) {
	return &testClient{}, nil
}

func (m mockDeviceCreator) setWGLink(w *wireguard, localSubnets []string) error {
	return nil
}
func (m mockDeviceCreator) addRoute(link netlink.Link, allowedIPs []net.IPNet) error {
	return nil
}
func (m mockDeviceCreator) delRoute(link netlink.Link, allowedIPs []net.IPNet) error {
	return nil
}

type testClient struct {
	DeviceFunc func(name string) (*wgtypes.Device, error)
	// Other functions can be overwritten too.
}

func (c *testClient) Close() error                                          { return nil }
func (c *testClient) Devices() ([]*wgtypes.Device, error)                   { return []*wgtypes.Device{}, nil }
func (c *testClient) Device(name string) (*wgtypes.Device, error)           { return c.DeviceFunc(name) }
func (c *testClient) ConfigureDevice(name string, cfg wgtypes.Config) error { return nil }
