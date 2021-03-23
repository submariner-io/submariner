/*
© 2021 Red Hat, Inc. and others.

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

package natdiscovery

import (
	"encoding/hex"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"google.golang.org/protobuf/proto"
	"k8s.io/klog"

	natproto "github.com/submariner-io/submariner/pkg/natdiscovery/proto"
)

func (nd *natDiscovery) runListener(stopCh <-chan struct{}) error {
	serverAddress, err := net.ResolveUDPAddr("udp4", ":"+strconv.Itoa(int(*nd.localEndpoint.Spec.NATDiscoveryPort)))
	if err != nil {
		return errors.Wrap(err, "Error resolving UDP address")
	}

	serverConnection, err := net.ListenUDP("udp4", serverAddress)
	if err != nil {
		return errors.Wrapf(err, "Error listening on udp port %d", *nd.localEndpoint.Spec.NATDiscoveryPort)
	}

	// Instead of storing the server connection I save the reference to the WriteToUDP
	// of our server connection instance, in a way that we can use this for unit testing
	// later too.
	nd.serverUDPWrite = serverConnection.WriteToUDP

	ticker := time.Tick(time.Second)

	go nd.listenForPeerMessagesOnConnection(serverConnection)

	go func() {
		for {
			select {
			case <-stopCh:
				serverConnection.Close()
				return
			case <-ticker:
				klog.V(log.DEBUG).Info("NAT checking endpoint list (1234)")
				nd.checkEndpointList()

			case message := <-nd.peerMessages:
				if err := nd.parseAndHandleMessageFromAddress(message.buf, message.addr, message.connection); err != nil {
					klog.Errorf("Error handling message from address %s: %s:\n%s", message.addr.String(), err,
						hex.Dump(message.buf))
				}
			}
		}
	}()

	return nil
}

func (nd *natDiscovery) listenForPeerMessagesOnConnection(connection *net.UDPConn) {
	var (
		err  error
		addr *net.UDPAddr
		len  int
	)

	buf := make([]byte, 2048)

	for err == nil {
		if len, addr, err = connection.ReadFromUDP(buf); err != nil {
			if !errors.Is(err, os.ErrDeadlineExceeded) {
				klog.Errorf("Error receiving from udp: %s", err)
			}
		}
		nd.peerMessages <- udpMessage{buf: buf[:len], addr: addr, connection: connection}
	}
	klog.Infof("UDP receiver finished with: %s", err)
}

func (nd *natDiscovery) parseAndHandleMessageFromAddress(buf []byte, addr *net.UDPAddr, connection *net.UDPConn) error {
	msg := natproto.SubmarinerNatDiscoveryMessage{}
	if err := proto.Unmarshal(buf, &msg); err != nil {
		return errors.Wrapf(err, "Error unmarshaling message received on UDP port %d", natproto.DefaultPort)
	}

	if request := msg.GetRequest(); request != nil {
		return nd.handleRequestFromAddress(request, addr, connection)
	} else if response := msg.GetResponse(); response != nil {
		return nd.handleResponseFromAddress(response, addr)
	}

	return errors.Errorf("Message without response or request received from %#v", addr)
}
