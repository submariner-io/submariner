package wireguard

import (
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/log"
	"github.com/vishvananda/netlink"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/types"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

const (
	// DefaultListenPort specifies UDP port address of wireguard
	DefaultListenPort = 5871

	// DefaultDeviceName specifies name of wireguard network device
	DefaultDeviceName = "subwg0"

	// PublicKey is name (key) of publicKey entry in back-end map
	PublicKey = "publicKey"

	// we assume Linux
	//deviceType = wgtypes.LinuxKernel

	cableDriverName = "wireguard"
)

func init() {
	// uncomment next line to set as default
	//cable.SetDefautCableDriver(cableDriverName)
	cable.AddDriver(cableDriverName, NewWGDriver)
}

type wireguard struct {
	localSubnets  []*net.IPNet
	localEndpoint types.SubmarinerEndpoint
	peers         map[string]wgtypes.Key // clusterID -> publicKey
	mutex         sync.Mutex
	client        *wgctrl.Client
	link          netlink.Link
	//debug   bool
	//logFile string
}

// NewWGDriver creates a new Wireguard driver
func NewWGDriver(localSubnets []string, localEndpoint types.SubmarinerEndpoint) (cable.Driver, error) {

	var err error

	wg := wireguard{
		peers:         make(map[string]wgtypes.Key),
		localEndpoint: localEndpoint,
	}

	// create the wg device if needed (ip link add dev $DefaultDeviceName type wireguard)
	if wg.link, err = netlink.LinkByName(DefaultDeviceName); err == nil {
		// delete existing device
		if err = netlink.LinkDel(wg.link); err != nil {
			return nil, fmt.Errorf("failed to delete existing wireguard device: %v", err)
		}
	}

	la := netlink.NewLinkAttrs()
	la.Name = DefaultDeviceName
	wg.link = &netlink.GenericLink{
		LinkAttrs: la,
		LinkType:  "wireguard",
	}
	if err = netlink.LinkAdd(wg.link); err != nil {
		return nil, fmt.Errorf("failed to add wireguard device: %v", err)
	}

	// setup local address (ip address add dev $DefaultDeviceName $PublicIP
	var ip string
	if localEndpoint.Spec.NATEnabled {
		ip = localEndpoint.Spec.PublicIP
	} else {
		ip = localEndpoint.Spec.PrivateIP
	}
	var localIP *netlink.Addr
	if localIP, err = netlink.ParseAddr(ip + "/32"); err != nil {
		// try again as CIDR
		if localIP, err = netlink.ParseAddr(ip); err != nil {
			return nil, fmt.Errorf("failed to parse my IP address %s: %v", ip, err)
		}
	}
	if err = netlink.AddrAdd(wg.link, localIP); err != nil {
		return nil, fmt.Errorf("failed to add local address: %v", err)
	}

	// check localSubnets
	var cidr *net.IPNet
	wg.localSubnets = make([]*net.IPNet, len(localSubnets))
	for i, sn := range localSubnets {
		if _, cidr, err = net.ParseCIDR(sn); err != nil {
			return nil, fmt.Errorf("failed to parse subnet %s: %v", sn, err)
		}
		wg.localSubnets[i] = cidr
	}

	// create controller
	if wg.client, err = wgctrl.New(); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("wgctrl is not available on this system")
		}
		return nil, fmt.Errorf("failed to open wgctl client: %v", err)
	}
	defer func() {
		if err != nil {
			if e := wg.client.Close(); e != nil {
				klog.Errorf("Failed to close client %v", e)
			}
			wg.client = nil
		}
	}()

	// generate local keys and set public key in BackendConfig
	var priv, pub wgtypes.Key
	if priv, err = wgtypes.GeneratePrivateKey(); err != nil {
		return nil, fmt.Errorf("error generating private key: %v", err)
	}
	pub = priv.PublicKey()
	if localEndpoint.Spec.BackendConfig == nil {
		localEndpoint.Spec.BackendConfig = make(map[string]string)
	}
	localEndpoint.Spec.BackendConfig[PublicKey] = pub.String()

	// configure the device. still not up
	port := DefaultListenPort
	peerConfigs := make([]wgtypes.PeerConfig, 0)
	cfg := wgtypes.Config{
		PrivateKey:   &priv,
		ListenPort:   &port,
		FirewallMark: nil,
		ReplacePeers: false,
		Peers:        peerConfigs,
	}
	if err = wg.client.ConfigureDevice(DefaultDeviceName, cfg); err != nil {
		return nil, fmt.Errorf("failed to configure wireguard device: %v", err)
	}

	klog.V(log.TRACE).Infof("Initialized wireguard %s with publicKey %s", DefaultDeviceName, pub.String())
	return &wg, nil
}

func (w *wireguard) Init() error {
	// ip link set $DefaultDeviceName up
	if err := netlink.LinkSetUp(w.link); err != nil {
		return fmt.Errorf("failed to bring up wireguard device: %v", err)
	}
	return nil
}

func (w *wireguard) ConnectToEndpoint(remoteEndpoint types.SubmarinerEndpoint) (string, error) {

	if w.localEndpoint.Spec.ClusterID == remoteEndpoint.Spec.ClusterID {
		klog.V(log.TRACE).Infof("Will not connect to self")
		return "", nil
	}

	var err error
	var found bool

	// remote addresses
	var ip string
	if remoteEndpoint.Spec.NATEnabled {
		ip = remoteEndpoint.Spec.PublicIP
	} else {
		ip = remoteEndpoint.Spec.PrivateIP
	}
	var remoteIP net.IP
	if remoteIP = net.ParseIP(ip); remoteIP == nil {
		return "", fmt.Errorf("failed to parse remote IP %s", ip)
	}

	// handle public key
	var remoteKey wgtypes.Key
	var key string
	if key, found = remoteEndpoint.Spec.BackendConfig[PublicKey]; !found {
		return "", fmt.Errorf("missing peer public key")
	}
	if remoteKey, err = wgtypes.ParseKey(key); err != nil {
		return "", fmt.Errorf("failed to parse public key %s: %v", key, err)
	}
	klog.V(log.TRACE).Infof("Connecting cluster %s endpoint %s with publicKey %s", remoteEndpoint.Spec.ClusterID, remoteIP.String(), remoteKey.String())
	w.mutex.Lock()
	defer w.mutex.Unlock()
	var oldKey wgtypes.Key
	if oldKey, found = w.peers[remoteEndpoint.Spec.ClusterID]; found {
		if oldKey.String() == remoteKey.String() {
			//TODO check that peer config has not changed (eg allowedIPs)
			klog.V(log.TRACE).Infof("Skipping update of existing peer key %s", oldKey.String())
			return ip, nil
		}
		// remove old
		klog.V(log.TRACE).Infof("Removing old key %s", oldKey.String())
		peerCfg := []wgtypes.PeerConfig{
			{
				PublicKey: remoteKey,
				Remove:    true,
			},
		}
		if err = w.client.ConfigureDevice(DefaultDeviceName, wgtypes.Config{
			ReplacePeers: true,
			Peers:        peerCfg,
		}); err != nil {
			klog.Errorf("Failed to remove old key %s: %v", oldKey.String(), err)
		}
		delete(w.peers, remoteEndpoint.Spec.ClusterID)
		klog.V(log.TRACE).Infof("Successfully removed old key %s", oldKey.String())
	}

	// Set peer subnets
	allowedIPs := make([]net.IPNet, len(remoteEndpoint.Spec.Subnets))
	var cidr *net.IPNet
	for i, sn := range remoteEndpoint.Spec.Subnets {
		if _, cidr, err = net.ParseCIDR(sn); err != nil {
			return "", fmt.Errorf("failed to parse subnet %s: %v", sn, err)
		}
		allowedIPs[i] = *cidr
	}

	// configure peer
	peerCfg := []wgtypes.PeerConfig{{
		PublicKey:    remoteKey,
		Remove:       false,
		UpdateOnly:   false,
		PresharedKey: nil,
		Endpoint: &net.UDPAddr{
			IP:   remoteIP,
			Port: DefaultListenPort,
		},
		PersistentKeepaliveInterval: nil,
		ReplaceAllowedIPs:           true,
		AllowedIPs:                  allowedIPs,
	}}
	if err = w.client.ConfigureDevice(DefaultDeviceName, wgtypes.Config{
		ReplacePeers: false,
		Peers:        peerCfg,
	}); err != nil {
		return "", fmt.Errorf("failed to configure peer: %v", err)
	}
	// verify peer was added
	var d *wgtypes.Device
	if d, err = w.client.Device(DefaultDeviceName); err != nil {
		klog.Errorf("Failed to find wireguard device by name: %v", err)
	} else {
		found = false
		for _, p := range d.Peers {
			if p.PublicKey.String() == remoteKey.String() {
				found = true
				klog.V(log.TRACE).Infof("Peer configured: %+v", p)
				break
			}
		}
		if !found {
			klog.Errorf("Failed to verify peer configuration")
		}
	}
	w.peers[remoteEndpoint.Spec.ClusterID] = remoteKey

	// Add routes to peer
	//TODO save old routes for removal
	var wg netlink.Link
	if wg, err = netlink.LinkByName(DefaultDeviceName); err != nil {
		return "", fmt.Errorf("failed to find wireguard link by name: %v", err)
	}
	for _, peerNet := range allowedIPs {
		route := netlink.Route{
			LinkIndex: wg.Attrs().Index,
			Dst:       &peerNet,
		}
		if err = netlink.RouteAdd(&route); err != nil {
			return "", fmt.Errorf("failed to add route %s: %v", route.String(), err)
		}
	}

	klog.V(log.TRACE).Infof("Successfully connected endpoint peer %+v", peerCfg)

	return ip, nil
}

func (w *wireguard) DisconnectFromEndpoint(remoteEndpoint types.SubmarinerEndpoint) error {
	klog.V(log.TRACE).Infof("Removing endpoint %v+", remoteEndpoint)

	if w.localEndpoint.Spec.ClusterID == remoteEndpoint.Spec.ClusterID {
		klog.V(log.TRACE).Infof("Will not disconnect self")
		return nil
	}
	var err error
	var found bool

	// public key
	var key string
	if key, found = remoteEndpoint.Spec.BackendConfig[PublicKey]; !found {
		return fmt.Errorf("missing peer public key")
	}
	var remoteKey wgtypes.Key
	if remoteKey, err = wgtypes.ParseKey(key); err != nil {
		return fmt.Errorf("failed to parse public key %s: %v", key, err)
	}
	w.mutex.Lock()
	defer w.mutex.Unlock()
	var oldKey wgtypes.Key
	keyMismatch := false
	if oldKey, found = w.peers[remoteEndpoint.Spec.ClusterID]; !found {
		keyMismatch = true
		klog.Warningf("Key mismatch, cluster %s has no key but asked to remove %s", remoteEndpoint.Spec.ClusterID, remoteKey.String())
	} else if oldKey.String() != remoteKey.String() {
		keyMismatch = true
		klog.Warningf("Key mismatch, cluster %s key is %s but asked to remove %s", remoteEndpoint.Spec.ClusterID, oldKey.String(), remoteKey.String())
	}

	// wg remove
	klog.V(log.TRACE).Infof("Removing wireguard peer with key %s", remoteKey.String())
	peerCfg := []wgtypes.PeerConfig{{
		PublicKey: remoteKey,
		Remove:    true,
	}}
	if err = w.client.ConfigureDevice(DefaultDeviceName, wgtypes.Config{
		ReplacePeers: false,
		Peers:        peerCfg,
	}); err != nil {
		return fmt.Errorf("failed to remove wireguard peer with key %s: %v", remoteKey.String(), err)
	}
	if keyMismatch {
		klog.Warningf("Key mismatch for peer cluster %s, keeping existing routes", remoteEndpoint.Spec.ClusterID)
		return nil
	}
	delete(w.peers, remoteEndpoint.Spec.ClusterID)

	// del routes
	var wg netlink.Link
	if wg, err = netlink.LinkByName(DefaultDeviceName); err != nil {
		return fmt.Errorf("failed to find wireguard device by name: %v", err)
	}
	var cidr *net.IPNet
	for _, sn := range remoteEndpoint.Spec.Subnets {
		if _, cidr, err = net.ParseCIDR(sn); err != nil {
			return fmt.Errorf("failed to parse subnet %s: %v", sn, err)
		}
		route := netlink.Route{
			LinkIndex: wg.Attrs().Index,
			Dst:       cidr,
		}
		if err = netlink.RouteDel(&route); err != nil {
			return fmt.Errorf("failed to delete route %s: %v", route.String(), err)
		}
	}

	klog.V(log.TRACE).Infof("Done removing endpoint for cluster %s", remoteEndpoint.Spec.ClusterID)
	return nil
}

func (w *wireguard) GetActiveConnections(clusterID string) ([]string, error) {
	// force caller to skip duplicate handling
	return make([]string, 0), nil
}
