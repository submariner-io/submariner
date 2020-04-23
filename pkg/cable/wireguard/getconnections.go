package wireguard

import (
	"fmt"
	"strconv"
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
		connection, err := w.updateConnectionForPeer(&p, cid)
		if err != nil {
			// this should not happen, we do not have the spec, so cannot create a new connection
			klog.Errorf("Found peer but no connection for cluster %s, ignoring: %v", cid, err)
			continue
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

func (w *wireguard) updateConnectionForPeer(p *wgtypes.Peer, cid string) (*v1.Connection, error) {
	connection, found := w.connections[cid]
	if !found {
		return nil, fmt.Errorf("failed to find connection for peer with ClusterId %s", cid)
	}
	now := time.Now().UnixNano()
	if peerTrafficDelta(connection, lastChecked, now) < KeepAliveInterval.Nanoseconds() {
		// too fast to see any change, leave status as is
		return connection, nil
	}
	tx := peerTrafficDelta(connection, transmitBytes, p.TransmitBytes)
	rx := peerTrafficDelta(connection, receiveBytes, p.ReceiveBytes)
	if tx > 0 || rx > 0 {
		connection.SetStatus(v1.Connected, "Rx=%d Bytes, Tx=%d Bytes", p.ReceiveBytes, p.TransmitBytes)
		return connection, nil
	}
	if p.LastHandshakeTime.IsZero() {
		connection.SetStatus(v1.Connecting, "no handshake yet")
		return connection, nil
	}
	silence := time.Since(p.LastHandshakeTime)
	if silence < KeepAliveInterval * 2 {
		connection.SetStatus(v1.Connecting, "handshake was %.1f seconds ago, no traffic yet",
			silence.Seconds())
		return connection, nil
	}
	if silence > handshakeTimeout {
		connection.SetStatus(v1.ConnectionError, "no handshake for %.1f seconds",
			silence.Seconds())
		return connection, nil
	}
	connection.SetStatus(v1.ConnectionError, "handshake, but no bytes sent or received for %.1f seconds",
		silence.Seconds())

	return connection, nil
}

func (w *wireguard) updatePeerStatus(key *wgtypes.Key, cid string) error {
	p, err := w.peerByKey(key)
	if err != nil {
		if connection, found := w.connections[cid]; found {
			connection.SetStatus(v1.ConnectionError, "cannot fetch status for peer %s: %v", key, err)
			return nil
		}
		return fmt.Errorf("failed to fetch peer configuration: %v", err)
	}
	if _, err := w.updateConnectionForPeer(p, cid); err != nil {
		return fmt.Errorf("failed to update connection status for peer %s of cluster %s: %v", key, cid, err)
	}
	return nil
}

func peerTrafficDelta(c *v1.Connection, key string, newVal int64) int64 {
	delta := int64(1) // if no parsable history default to not zero
	if s, found := c.Endpoint.BackendConfig[key]; found {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			delta = newVal - i
		}
	}
	c.Endpoint.BackendConfig[key] = strconv.FormatInt(newVal, 10)
	return delta
}

