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
		connection, err := w.connectionByKey(&key)
		if err != nil {
			klog.Warningf("Found unknown peer with key %s, removing", key)
			if err := w.removePeer(&key); err != nil {
				klog.Errorf("Could not delete WireGuard peer with key %s, ignoring: %v", key, err)
			}
			continue
		}
		w.updateConnectionForPeer(&p, connection)
		connections = append(connections, *connection.DeepCopy())
	}
	return &connections, nil
}

func (w *wireguard) connectionByKey(key *wgtypes.Key) (*v1.Connection, error) {
	for cid, con := range w.connections {
		if k, err := keyFromSpec(&con.Endpoint); err == nil {
			if key.String() == k.String() {
				return con, nil
			}
		} else {
			klog.Errorf("Could not compare key for cluster %s, skipping: %v", cid, err)
		}
	}
	return nil, fmt.Errorf("connection not found for key %s", key)
}

// update logic, based on delta from last check
// good state requires a handshake and traffic
// if no handshake or stale handshake
func (w *wireguard) updateConnectionForPeer(p *wgtypes.Peer, connection *v1.Connection) {
	now := int64(time.Nanosecond) * time.Now().UnixNano() / int64(time.Millisecond)
	keepAliveMS := KeepAliveInterval.Milliseconds() + 2 // +2 for rounding

	lc := peerTrafficDelta(connection, lastChecked, now)
	lcSec := time.Duration(int64(time.Millisecond) * lc).Seconds()
	if lc < keepAliveMS {
		// too fast to see any change, leave status as is
		return
	}

	// longer than keep-alive interval, expect bytes>0
	tx := peerTrafficDelta(connection, transmitBytes, p.TransmitBytes)
	rx := peerTrafficDelta(connection, receiveBytes, p.ReceiveBytes)

	if p.LastHandshakeTime.IsZero() {
		if lc > handshakeTimeout.Milliseconds() {
			// no initial handshake for too long
			connection.SetStatus(v1.ConnectionError, "no initial handshake for %.1f seconds", lcSec)
			return
		}
		if tx > 0 || rx > 0 {
			// no handshake, but at least some communication in progress
			connection.SetStatus(v1.Connecting, "no initial handshake yet")
			return
		}
	}
	if tx > 0 || rx > 0 {
		// all is good
		connection.SetStatus(v1.Connected, "Rx=%d Bytes, Tx=%d Bytes", p.ReceiveBytes, p.TransmitBytes)
		savePeerTraffic(connection, now, p.TransmitBytes, p.ReceiveBytes)
		return
	}
	handshakeDelta := time.Since(p.LastHandshakeTime)
	if handshakeDelta > handshakeTimeout {
		// hard error, really long time since handshake
		connection.SetStatus(v1.ConnectionError, "no handshake for %.1f seconds",
			handshakeDelta.Seconds())
		return
	}
	if lc < 2*keepAliveMS {
		// grace period, leave status unchanged
		klog.Warningf("No traffic for connection; handshake was %.1f seconds ago: %v", lcSec,
			connection)
		return
	}
	// soft error, no traffic, stale handshake
	connection.SetStatus(v1.ConnectionError, "no bytes sent or received for %.1f seconds",
		lcSec)
}

func (w *wireguard) updatePeerStatus(c *v1.Connection, key *wgtypes.Key) {
	p, err := w.peerByKey(key)
	if err != nil {
		c.SetStatus(v1.ConnectionError, "cannot fetch status for peer %s: %v", key, err)
		return
	}
	w.updateConnectionForPeer(p, c)
}

// Compare backendConfig[key] or initialize entry
// Return delta from previous value or 0 if no previous value
func peerTrafficDelta(c *v1.Connection, key string, newVal int64) int64 {
	s, found := c.Endpoint.BackendConfig[key]
	if found {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return newVal - i
		}
	}
	c.Endpoint.BackendConfig[key] = strconv.FormatInt(newVal, 10)
	return 0
}

// Save backendConfig[key]
func savePeerTraffic(c *v1.Connection, lc int64, tx int64, rx int64) {
	c.Endpoint.BackendConfig[lastChecked] = strconv.FormatInt(lc, 10)
	c.Endpoint.BackendConfig[transmitBytes] = strconv.FormatInt(tx, 10)
	c.Endpoint.BackendConfig[receiveBytes] = strconv.FormatInt(rx, 10)
}
