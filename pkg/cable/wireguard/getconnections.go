package wireguard

import (
	"fmt"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"k8s.io/klog"

	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

func (w *wireguard) GetConnections() (*[]v1.Connection, error) {
	d, err := w.client.Device(DefaultDeviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to find device %s: %v", DefaultDeviceName, err)
	}
	connections := make([]v1.Connection, 0)

	w.mutex.Lock()
	defer w.mutex.Unlock()

	for _, p := range d.Peers {
		key := p.PublicKey
		cid, err := w.clusterIDByPeer(&key)
		if err != nil {
			klog.Warningf("Found unknown peer with key %s, removing", key)
			if err2 := w.removePeer(&key); err2 != nil {
				klog.Errorf("Could not delete WireGuard peer with key %s, ignoring: %v", key, err)
			}
			continue
		}
		connection, found := w.connections[cid]
		if !found {
			// This should not happen -- peer and connection are set together
			// TODO delete WireGuard peer entry and peer
			return nil, fmt.Errorf("failed to find connection for peer with ClusterId %s", key)
		}
		if err := updateConnectionForPeer(connection, &p); err != nil {
			connection.SetStatus(v1.ConnectionError, "Cannot update status for cluster id %s: %v",
				cid, err)
		}
		connections = append(connections, *connection)
	}
	return &connections, nil
}

func (w *wireguard) clusterIDByPeer(key *wgtypes.Key) (string, error) {
	for cid, k := range w.peers {
		if key.String() == k.String() {
			return cid, nil
		}
	}
	return "", fmt.Errorf("cluser id not found for key %s", key)
}

func updateConnectionForPeer(connection *v1.Connection, p *wgtypes.Peer) error {
	silence := time.Since(p.LastHandshakeTime)
	if silence > (3 * KeepAliveInterval) {
		connection.SetStatus(v1.ConnectionError, "connection has been silent for %.1f seconds",
			silence.Seconds())
		return nil
	}
	connection.SetStatus(v1.Connected,
		"last handshake was %.1f seconds ago, Rx=%d Bytes, Tx=%d Bytes",
		silence.Seconds(), p.ReceiveBytes, p.TransmitBytes)

	// TODO compare spec
	return nil
}

func (w *wireguard) updatePeerStatus(key *wgtypes.Key, connection *v1.Connection) error {
	p, err := w.peerByKey(key)
	if err != nil {
		connection.SetStatus(v1.ConnectionError, "cannot fetch status for peer %s: %v", key, err)
		return fmt.Errorf("failed to fetch peer configuration: %v", err)
	}
	if err := updateConnectionForPeer(connection, p); err != nil {
		connection.SetStatus(v1.ConnectionError, "cannot update status for peer %s: %v", key, err)
		return fmt.Errorf("failed to update connection status: %v", err)
	}
	return nil
}
