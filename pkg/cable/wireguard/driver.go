package wireguard

import (
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"

	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/log"
	"github.com/vishvananda/netlink"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/types"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

const (
	// DefaultListenPort specifies UDP port address of WireGuard
	DefaultListenPort = 5871

	// DefaultDeviceName specifies name of WireGuard network device
	DefaultDeviceName = "subwg0"

	// PublicKey is name (key) of publicKey entry in back-end map
	PublicKey = "publicKey"

	// KeepAliveInterval to use for wg peers
	KeepAliveInterval = 10 * time.Second

	// handshakeTimeout is maximal time from handshake a connections is still considered connected
	handshakeTimeout = 2*time.Minute + 10*time.Second

	//TODO generalize cleanStrongswanRoutingTable, for now, must use 220
	routingTable = 220

	cableDriverName = "wireguard"
	receiveBytes    = "ReceiveBytes"  // for peer connection status
	transmitBytes   = "TransmitBytes" // for peer connection status
	lastChecked     = "LastChecked"   // for connection peer status
)

func init() {
	cable.AddDriver(cableDriverName, NewDriver)
}

type wireguard struct {
	localSubnets  []net.IPNet
	localEndpoint types.SubmarinerEndpoint
	connections   map[string]*v1.Connection // clusterID -> remote ep connection
	mutex         sync.Mutex
	client        *wgctrl.Client
	link          netlink.Link
}

// NewDriver creates a new WireGuard driver
func NewDriver(localSubnets []string, localEndpoint types.SubmarinerEndpoint) (cable.Driver, error) {
	var err error

	w := wireguard{
		connections:   make(map[string]*v1.Connection),
		localEndpoint: localEndpoint,
	}

	if err = w.setWGLink(localSubnets); err != nil {
		return nil, fmt.Errorf("failed to setup WireGuard link: %v", err)
	}

	// create controller
	if w.client, err = wgctrl.New(); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("wgctrl is not available on this system")
		}
		return nil, fmt.Errorf("failed to open wgctl client: %v", err)
	}
	defer func() {
		if err != nil {
			if e := w.client.Close(); e != nil {
				klog.Errorf("Failed to close client %v", e)
			}
			w.client = nil
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
		ReplacePeers: true,
		Peers:        peerConfigs,
	}
	if err = w.client.ConfigureDevice(DefaultDeviceName, cfg); err != nil {
		return nil, fmt.Errorf("failed to configure WireGuard device: %v", err)
	}

	klog.V(log.DEBUG).Infof("Initialized WireGuard %s with publicKey %s", DefaultDeviceName, pub)
	return &w, nil
}

func (w *wireguard) Init() error {
	// ip link set $DefaultDeviceName up
	if err := netlink.LinkSetUp(w.link); err != nil {
		return fmt.Errorf("failed to bring up WireGuard device: %v", err)
	}
	return nil
}

func (w *wireguard) GetName() string {
	return cableDriverName
}

func (w *wireguard) ConnectToEndpoint(remoteEndpoint types.SubmarinerEndpoint) (string, error) {
	if w.localEndpoint.Spec.ClusterID == remoteEndpoint.Spec.ClusterID {
		klog.V(log.DEBUG).Infof("Will not connect to self")
		return "", nil
	}

	// parse remote addresses and allowed IPs
	ip := endpointIP(&remoteEndpoint)
	remoteIP := net.ParseIP(ip)
	if remoteIP == nil {
		return "", fmt.Errorf("failed to parse remote IP %s", ip)
	}
	allowedIPs := parseSubnets(remoteEndpoint.Spec.Subnets)

	// parse remote public key
	remoteKey, err := keyFromSpec(&remoteEndpoint.Spec)
	if err != nil {
		return "", fmt.Errorf("failed to parse peer public key: %v", err)
	}

	klog.V(log.DEBUG).Infof("Connecting cluster %s endpoint %s with publicKey %s",
		remoteEndpoint.Spec.ClusterID, remoteIP, remoteKey)
	w.mutex.Lock()
	defer w.mutex.Unlock()

	// delete or update old peers for ClusterID
	oldCon, found := w.connections[remoteEndpoint.Spec.ClusterID]
	if found {
		if oldKey, err := keyFromSpec(&oldCon.Endpoint); err == nil {
			if oldKey.String() == remoteKey.String() {
				// existing connection, update status and skip
				w.updatePeerStatus(oldCon, oldKey)
				klog.V(log.DEBUG).Infof("Skipping connect for existing peer key %s", oldKey)
				return ip, nil
			}
			// new peer will take over subnets so can ignore error
			_ = w.removePeer(oldKey)
		}
		delete(w.connections, remoteEndpoint.Spec.ClusterID)
	}

	// create connection, overwrite existing connection
	connection := v1.NewConnection(remoteEndpoint.Spec)
	connection.SetStatus(v1.Connecting, "Connection has been created but not yet started")
	klog.V(log.DEBUG).Infof("Adding connection for cluster %s, %v", remoteEndpoint.Spec.ClusterID, connection)
	w.connections[remoteEndpoint.Spec.ClusterID] = connection

	// configure peer
	ka := KeepAliveInterval
	peerCfg := []wgtypes.PeerConfig{{
		PublicKey:    *remoteKey,
		Remove:       false,
		UpdateOnly:   false,
		PresharedKey: nil,
		Endpoint: &net.UDPAddr{
			IP:   remoteIP,
			Port: DefaultListenPort,
		},
		PersistentKeepaliveInterval: &ka,
		ReplaceAllowedIPs:           true,
		AllowedIPs:                  allowedIPs,
	}}
	err = w.client.ConfigureDevice(DefaultDeviceName, wgtypes.Config{
		ReplacePeers: false,
		Peers:        peerCfg,
	})
	if err != nil {
		return "", fmt.Errorf("failed to configure peer: %v", err)
	}

	// verify peer was added
	if p, err := w.peerByKey(remoteKey); err != nil {
		klog.Errorf("Failed to verify peer configuration: %v", err)
	} else {
		// TODO verify configuration
		klog.V(log.DEBUG).Infof("Peer configured: %+v", p)
	}

	// Add routes to peer
	//TODO save old routes for removal
	idx := w.link.Attrs().Index
	for _, peerNet := range allowedIPs {
		route := netlink.Route{
			LinkIndex: idx,
			Dst:       &peerNet,
			Table:     routingTable,
		}
		if err = netlink.RouteAdd(&route); err != nil {
			return "", fmt.Errorf("failed to add route %s: %v", route, err)
		}
	}

	klog.V(log.DEBUG).Infof("Done connecting endpoint peer %+v", peerCfg)
	return ip, nil
}

func keyFromSpec(ep *v1.EndpointSpec) (*wgtypes.Key, error) {
	s, found := ep.BackendConfig[PublicKey]
	if !found {
		return nil, fmt.Errorf("endpoint is missing public key")
	}
	key, err := wgtypes.ParseKey(s)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key %s: %v", s, err)
	}
	return &key, nil
}

func (w *wireguard) DisconnectFromEndpoint(remoteEndpoint types.SubmarinerEndpoint) error {
	klog.V(log.DEBUG).Infof("Removing endpoint %v+", remoteEndpoint)

	if w.localEndpoint.Spec.ClusterID == remoteEndpoint.Spec.ClusterID {
		klog.V(log.DEBUG).Infof("Will not disconnect self")
		return nil
	}

	// parse remote public key
	remoteKey, err := keyFromSpec(&remoteEndpoint.Spec)
	if err != nil {
		return fmt.Errorf("failed to parse peer public key: %v", err)
	}

	// wg remove
	_ = w.removePeer(remoteKey)

	w.mutex.Lock()
	defer w.mutex.Unlock()

	if w.keyMismatch(remoteEndpoint.Spec.ClusterID, remoteKey) {
		// ClusterID probably already associated with new spec. Do not remove connections entry nor routes
		klog.Warningf("Key mismatch for peer cluster %s, keeping existing routes and spec",
			remoteEndpoint.Spec.ClusterID)
		return nil
	}

	delete(w.connections, remoteEndpoint.Spec.ClusterID)

	// del routes
	allowedIPs := parseSubnets(remoteEndpoint.Spec.Subnets)
	idx := w.link.Attrs().Index
	for _, peerNet := range allowedIPs {
		route := netlink.Route{
			LinkIndex: idx,
			Dst:       &peerNet,
			Table:     routingTable,
		}
		if err = netlink.RouteDel(&route); err != nil {
			return fmt.Errorf("failed to delete route %s: %v", route, err)
		}
	}

	klog.V(log.DEBUG).Infof("Done removing endpoint for cluster %s", remoteEndpoint.Spec.ClusterID)
	return nil
}

func (w *wireguard) GetActiveConnections(clusterID string) ([]string, error) {
	// force caller to skip duplicate handling
	return make([]string, 0), nil
}

// Create new wg link and assign addr from local subnets
func (w *wireguard) setWGLink(localSubnets []string) error {
	// create routing table
	if err := setRoutingTable(); err != nil {
		return fmt.Errorf("failed to create routing table: %v", err)
	}

	// delete existing wg device if needed
	if link, err := netlink.LinkByName(DefaultDeviceName); err == nil {
		// delete existing device
		if err := netlink.LinkDel(link); err != nil {
			return fmt.Errorf("failed to delete existing WireGuard device: %v", err)
		}
	}

	// create the wg device (ip link add dev $DefaultDeviceName type wireguard)
	la := netlink.NewLinkAttrs()
	la.Name = DefaultDeviceName
	link := &netlink.GenericLink{
		LinkAttrs: la,
		LinkType:  "wireguard",
	}
	if err := netlink.LinkAdd(link); err == nil {
		w.link = link
	} else {
		return fmt.Errorf("failed to add WireGuard device: %v", err)
	}

	// parse localSubnets and get internal address
	w.localSubnets = parseSubnets(localSubnets)
	ip, err := discoverInternalIP(w.localSubnets)
	if err != nil {
		klog.Errorf("Error while attempting to discover internal IP: %v", err)
	}
	if ip == "" {
		klog.V(log.DEBUG).Infof("Using endpoint IP as internal address; %v", err)
		ip = endpointIP(&w.localEndpoint)
	}
	klog.V(log.DEBUG).Infof("Setting interface address to  %s", ip)

	// setup local address (ip address add dev $DefaultDeviceName $PublicIP
	localIP, err := netlink.ParseAddr(ip + "/32")
	if err != nil {
		// try again as CIDR
		if localIP, err = netlink.ParseAddr(ip); err != nil {
			return fmt.Errorf("failed to parse IP address %s: %v", ip, err)
		}
	}
	if err = netlink.AddrAdd(w.link, localIP); err != nil {
		return fmt.Errorf("failed to add local address: %v", err)
	}

	return nil
}

// find internal host IP inside one of the local CIDRs
// TODO move this to utils
func discoverInternalIP(cidrs []net.IPNet) (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", fmt.Errorf("net.InterfaceAddrs() returned error : %v", err)
	}

	for _, a := range addrs {
		ip, _, err := net.ParseCIDR(a.String())
		if err != nil {
			klog.V(log.DEBUG).Infof("Skipping local address %v, unable to ParseCIDR: %v", a, err)
			continue
		}
		if ip.To4() == nil {
			klog.V(log.DEBUG).Infof("Skipping local address %+v: not IP4", ip)
			continue
		}
		for _, c := range cidrs {
			if c.Contains(ip) {
				return ip.String(), nil
			}
		}
	}
	klog.Warningf("Could not find an internal address in %v that matches a local subnet in %v", addrs, cidrs)
	return "", nil
}

// parse CIDR string and skip errors
func parseSubnets(subnets []string) []net.IPNet {
	nets := make([]net.IPNet, 0, len(subnets))
	for _, sn := range subnets {
		_, cidr, err := net.ParseCIDR(sn)
		if err != nil {
			// this should not happen. Log and continue
			klog.Errorf("failed to parse subnet %s: %v", sn, err)
			continue
		}
		nets = append(nets, *cidr)
	}
	return nets
}

func (w *wireguard) removePeer(key *wgtypes.Key) error {
	klog.V(log.DEBUG).Infof("Removing WireGuard peer with key %s", key)
	peerCfg := []wgtypes.PeerConfig{
		{
			PublicKey: *key,
			Remove:    true,
		},
	}
	err := w.client.ConfigureDevice(DefaultDeviceName, wgtypes.Config{
		ReplacePeers: false,
		Peers:        peerCfg,
	})
	if err != nil {
		klog.Errorf("Failed to remove WireGuard peer with key %s: %v", key, err)
		return err
	}
	klog.V(log.DEBUG).Infof("Done removing WireGuard peer with key %s", key)
	return nil
}

func (w *wireguard) peerByKey(key *wgtypes.Key) (*wgtypes.Peer, error) {
	d, err := w.client.Device(DefaultDeviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to find device %s: %v", DefaultDeviceName, err)
	}
	for _, p := range d.Peers {
		if p.PublicKey.String() == key.String() {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("peer not found for key %s", key)
}

// find if key matches connection spec (from spec clusterID)
func (w *wireguard) keyMismatch(cid string, key *wgtypes.Key) bool {
	c, found := w.connections[cid]
	if !found {
		klog.Warningf("Could not find spec for cluster %s, mismatched endpoint key %s", cid, key)
		return true
	}
	oldKey, err := keyFromSpec(&c.Endpoint)
	if err != nil {
		klog.Warningf("Could not find old key of cluster %s, mismatched endpoint key %s", cid, key)
		return true
	}
	if oldKey.String() != key.String() {
		klog.Warningf("Key mismatch, cluster %s key is %s, endpoint key is %s", cid, oldKey, key)
		return true
	}
	return false
}

func endpointIP(ep *types.SubmarinerEndpoint) string {
	if ep.Spec.NATEnabled {
		return ep.Spec.PublicIP
	}
	return ep.Spec.PrivateIP
}

func setRoutingTable() error {
	r := netlink.NewRule()
	r.Table = routingTable
	r.Priority = routingTable
	err := netlink.RuleAdd(r)
	if err == nil || os.IsExist(err) {
		return nil
	}
	return fmt.Errorf("could not add rule for routing table: %v", err)
}
