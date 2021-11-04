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

package wireguard

import (
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"k8s.io/klog"
)

const (
	// DefaultDeviceName specifies name of WireGuard network device.
	DefaultDeviceName = "submariner"

	// PublicKey is name (key) of publicKey entry in back-end map.
	PublicKey = "publicKey"

	// KeepAliveInterval to use for wg peers.
	KeepAliveInterval = 10 * time.Second

	// handshakeTimeout is maximal time from handshake a connections is still considered connected.
	handshakeTimeout = 2*time.Minute + 10*time.Second

	cableDriverName = "wireguard"
	receiveBytes    = "ReceiveBytes"  // for peer connection status
	transmitBytes   = "TransmitBytes" // for peer connection status
	lastChecked     = "LastChecked"   // for connection peer status

	// TODO use submariner prefix.
	specEnvPrefix = "ce_ipsec"
)

func init() {
	cable.AddDriver(cableDriverName, NewDriver)
}

type specification struct {
	PSK      string `default:"default psk"`
	NATTPort int    `default:"4500"`
}

type wireguard struct {
	localEndpoint types.SubmarinerEndpoint
	connections   map[string]*v1.Connection // clusterID -> remote ep connection
	mutex         sync.Mutex
	client        *wgctrl.Client
	link          netlink.Link
	spec          *specification
	psk           *wgtypes.Key
}

// NewDriver creates a new WireGuard driver.
func NewDriver(localEndpoint *types.SubmarinerEndpoint, localCluster *types.SubmarinerCluster) (cable.Driver, error) {
	// We'll panic if localEndpoint or localCluster are nil, this is intentional
	var err error

	w := wireguard{
		connections:   make(map[string]*v1.Connection),
		localEndpoint: *localEndpoint,
		spec:          new(specification),
	}

	if err := envconfig.Process(specEnvPrefix, w.spec); err != nil {
		return nil, errors.Wrap(err, "error processing environment config for wireguard")
	}

	if err = w.setWGLink(); err != nil {
		return nil, errors.Wrap(err, "failed to setup WireGuard link")
	}

	// Create the controller.
	if w.client, err = wgctrl.New(); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("wgctrl is not available on this system")
		}

		return nil, errors.Wrap(err, "failed to open wgctl client")
	}

	defer func() {
		if err != nil {
			if e := w.client.Close(); e != nil {
				klog.Errorf("Failed to close client %v", e)
			}

			w.client = nil
		}
	}()

	// Generate local keys and set public key in BackendConfig.
	var priv, pub, psk wgtypes.Key

	if psk, err = genPsk(w.spec.PSK); err != nil {
		return nil, errors.Wrap(err, "error generating pre-shared key")
	}

	w.psk = &psk

	if priv, err = wgtypes.GeneratePrivateKey(); err != nil {
		return nil, errors.Wrap(err, "error generating private key")
	}

	pub = priv.PublicKey()

	w.localEndpoint.Spec.BackendConfig[PublicKey] = pub.String()

	port, err := localEndpoint.Spec.GetBackendPort(v1.UDPPortConfig, int32(w.spec.NATTPort))
	if err != nil {
		return nil, errors.Wrapf(err, "error parsing %q from local endpoint", v1.UDPPortConfig)
	}

	portInt := int(port)

	// Configure the device - still not up.
	peerConfigs := make([]wgtypes.PeerConfig, 0)
	cfg := wgtypes.Config{
		PrivateKey:   &priv,
		ListenPort:   &portInt,
		FirewallMark: nil,
		ReplacePeers: true,
		Peers:        peerConfigs,
	}
	if err = w.client.ConfigureDevice(DefaultDeviceName, cfg); err != nil {
		return nil, errors.Wrap(err, "failed to configure WireGuard device")
	}

	klog.V(log.DEBUG).Infof("Created WireGuard %s with publicKey %s", DefaultDeviceName, pub)

	return &w, nil
}

func (w *wireguard) Init() error {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	klog.V(log.DEBUG).Infof("Initializing WireGuard device for cluster %s", w.localEndpoint.Spec.ClusterID)

	if len(w.connections) != 0 {
		return fmt.Errorf("cannot initialize with existing connections: %+v", w.connections)
	}

	l, err := net.InterfaceByName(DefaultDeviceName)
	if err != nil {
		return errors.Wrapf(err, "cannot get wireguard link by name %s", DefaultDeviceName)
	}

	d, err := w.client.Device(DefaultDeviceName)
	if err != nil {
		return errors.Wrap(err, "wgctrl cannot find WireGuard device")
	}

	k, err := keyFromSpec(&w.localEndpoint.Spec)
	if err != nil {
		return errors.Wrapf(err, "endpoint is missing public key %s", d.PublicKey)
	}

	if k.String() != d.PublicKey.String() {
		return fmt.Errorf("endpoint public key %s is different from device key %s", k, d.PublicKey)
	}

	// IP link set $DefaultDeviceName up.
	if err := netlink.LinkSetUp(w.link); err != nil {
		return errors.Wrap(err, "failed to bring up WireGuard device")
	}

	klog.V(log.DEBUG).Infof("WireGuard device %s, is up on i/f number %d, listening on port :%d, with key %s",
		w.link.Attrs().Name, l.Index, d.ListenPort, d.PublicKey)

	return nil
}

func (w *wireguard) GetName() string {
	return cableDriverName
}

func (w *wireguard) ConnectToEndpoint(endpointInfo *natdiscovery.NATEndpointInfo) (string, error) {
	// We'll panic if endpointInfo is nil, this is intentional
	remoteEndpoint := &endpointInfo.Endpoint
	ip := endpointInfo.UseIP

	if w.localEndpoint.Spec.ClusterID == remoteEndpoint.Spec.ClusterID {
		klog.V(log.DEBUG).Infof("Will not connect to self")
		return "", nil
	}

	// Parse remote addresses and allowed IPs.
	remoteIP := net.ParseIP(ip)
	if remoteIP == nil {
		return "", fmt.Errorf("failed to parse remote IP %s", ip)
	}

	allowedIPs := parseSubnets(remoteEndpoint.Spec.Subnets)

	// Parse remote public key.
	remoteKey, err := keyFromSpec(&remoteEndpoint.Spec)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse peer public key")
	}

	klog.V(log.DEBUG).Infof("Connecting cluster %s endpoint %s with publicKey %s",
		remoteEndpoint.Spec.ClusterID, remoteIP, remoteKey)
	w.mutex.Lock()
	defer w.mutex.Unlock()

	// Delete or update old peers for ClusterID.
	oldCon, found := w.connections[remoteEndpoint.Spec.ClusterID]
	if found {
		if oldKey, err := keyFromSpec(&oldCon.Endpoint); err == nil {
			if oldKey.String() == remoteKey.String() {
				// Existing connection, update status and skip.
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
	connection := v1.NewConnection(remoteEndpoint.Spec, ip, endpointInfo.UseNAT)
	connection.SetStatus(v1.Connecting, "Connection has been created but not yet started")
	klog.V(log.DEBUG).Infof("Adding connection for cluster %s, %v", remoteEndpoint.Spec.ClusterID, connection)
	w.connections[remoteEndpoint.Spec.ClusterID] = connection

	port, err := remoteEndpoint.Spec.GetBackendPort(v1.UDPPortConfig, int32(w.spec.NATTPort))
	if err != nil {
		klog.Warningf("Error parsing %q from remote endpoint %q - using port %dº instead: %v", v1.UDPPortConfig,
			remoteEndpoint.Spec.CableName, w.spec.NATTPort, err)
	}

	remotePort := int(port)

	// configure peer
	ka := KeepAliveInterval
	peerCfg := []wgtypes.PeerConfig{{
		PublicKey:    *remoteKey,
		Remove:       false,
		UpdateOnly:   false,
		PresharedKey: w.psk,
		Endpoint: &net.UDPAddr{
			IP:   remoteIP,
			Port: remotePort,
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
		return "", errors.Wrap(err, "failed to configure peer")
	}

	// verify peer was added
	if p, err := w.peerByKey(remoteKey); err != nil {
		klog.Errorf("Failed to verify peer configuration: %v", err)
	} else {
		// TODO verify configuration
		klog.V(log.DEBUG).Infof("Peer configured, PubKey:%s, EndPoint:%s, AllowedIPs:%v", p.PublicKey, p.Endpoint, p.AllowedIPs)
	}

	klog.V(log.DEBUG).Infof("Done connecting endpoint peer %s@%s", *remoteKey, remoteIP)

	cable.RecordConnection(cableDriverName, &w.localEndpoint.Spec, &connection.Endpoint, string(v1.Connected), true)

	return ip, nil
}

func keyFromSpec(ep *v1.EndpointSpec) (*wgtypes.Key, error) {
	s, found := ep.BackendConfig[PublicKey]
	if !found {
		return nil, fmt.Errorf("endpoint is missing public key")
	}

	key, err := wgtypes.ParseKey(s)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse public key %s", s)
	}

	return &key, nil
}

func (w *wireguard) DisconnectFromEndpoint(remoteEndpoint *types.SubmarinerEndpoint) error {
	// We'll panic if remoteEndpoint is nil, this is intentional
	klog.V(log.DEBUG).Infof("Removing endpoint %v+", remoteEndpoint)

	if w.localEndpoint.Spec.ClusterID == remoteEndpoint.Spec.ClusterID {
		klog.V(log.DEBUG).Infof("Will not disconnect self")
		return nil
	}

	// parse remote public key
	remoteKey, err := keyFromSpec(&remoteEndpoint.Spec)
	if err != nil {
		return errors.Wrap(err, "failed to parse peer public key")
	}

	// wg remove
	_ = w.removePeer(remoteKey)

	w.mutex.Lock()
	defer w.mutex.Unlock()

	if w.keyMismatch(remoteEndpoint.Spec.ClusterID, remoteKey) {
		// ClusterID probably already associated with new spec. Do not remove connections.
		klog.Warningf("Key mismatch for peer cluster %s, keeping existing spec", remoteEndpoint.Spec.ClusterID)
		return nil
	}

	delete(w.connections, remoteEndpoint.Spec.ClusterID)

	klog.V(log.DEBUG).Infof("Done removing endpoint for cluster %s", remoteEndpoint.Spec.ClusterID)
	cable.RecordDisconnected(cableDriverName, &w.localEndpoint.Spec, &remoteEndpoint.Spec)

	return nil
}

func (w *wireguard) GetActiveConnections() ([]v1.Connection, error) {
	// force caller to skip duplicate handling
	return make([]v1.Connection, 0), nil
}

// Create new wg link and assign addr from local subnets.
func (w *wireguard) setWGLink() error {
	// delete existing wg device if needed
	if link, err := netlink.LinkByName(DefaultDeviceName); err == nil {
		// delete existing device
		if err := netlink.LinkDel(link); err != nil {
			return errors.Wrap(err, "failed to delete existing WireGuard device")
		}
	}

	// Create the wg device (ip link add dev $DefaultDeviceName type wireguard).
	la := netlink.NewLinkAttrs()
	la.Name = DefaultDeviceName
	link := &netlink.GenericLink{
		LinkAttrs: la,
		LinkType:  "wireguard",
	}
	if err := netlink.LinkAdd(link); err == nil {
		w.link = link
	} else {
		return errors.Wrap(err, "failed to add WireGuard device")
	}

	return nil
}

// Parse CIDR string and skip errors.
func parseSubnets(subnets []string) []net.IPNet {
	nets := make([]net.IPNet, 0, len(subnets))

	for _, sn := range subnets {
		_, cidr, err := net.ParseCIDR(sn)
		if err != nil {
			// This should not happen. Log and continue.
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
		return nil, fmt.Errorf("failed to find device %s: %w", DefaultDeviceName, err)
	}
	for i := range d.Peers {
		if d.Peers[i].PublicKey.String() == key.String() {
			return &d.Peers[i], nil
		}
	}

	return nil, fmt.Errorf("peer not found for key %s", key)
}

// Find if key matches connection spec (from spec clusterID).
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

func genPsk(psk string) (wgtypes.Key, error) {
	// Convert spec PSK string to right length byte array, using sha256.Size == wgtypes.KeyLen.
	pskBytes := sha256.Sum256([]byte(psk))
	return wgtypes.NewKey(pskBytes[:])
}
