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
	"strconv"

	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
	"k8s.io/klog"

	natproto "github.com/submariner-io/submariner/pkg/natdiscovery/proto"
)

func (nd *natDiscovery) runListener(stopCh <-chan struct{}) error {
	if nd.localEndpoint.Spec.NATDiscoveryPort == nil {
		klog.Infof("NAT discovery protocol port not set for this gateway")
		return nil
	}

	serverConnection, err := createServerConnection(int(*nd.localEndpoint.Spec.NATDiscoveryPort))
	if err != nil {
		return err
	}

	// Instead of storing the server connection I save the reference to the WriteToUDP
	// of our server connection instance, in a way that we can use this for unit testing
	// later too.
	nd.serverUDPWrite = serverConnection.WriteToUDP

	go func() {
		<-stopCh
		serverConnection.Close()
	}()

	go nd.listenerLoop(serverConnection)

	return nil
}

func createServerConnection(port int) (*net.UDPConn, error) {
	serverAddress, err := net.ResolveUDPAddr("udp4", ":"+strconv.Itoa(port))
	if err != nil {
		return nil, errors.Wrap(err, "Error resolving UDP address")
	}

	serverConnection, err := net.ListenUDP("udp4", serverAddress)
	if err != nil {
		return nil, errors.Wrapf(err, "Error listening on udp port %d", port)
	}

	return serverConnection, nil
}

func (nd *natDiscovery) listenerLoop(serverConnection *net.UDPConn) {
	buf := make([]byte, 2048)

	for {
		length, addr, err := serverConnection.ReadFromUDP(buf)
		if length == 0 {
			klog.Info("Stopping NAT listener")
			return
		} else if err != nil {
			klog.Errorf("Error receiving from udp: %s", err)
		} else if err := nd.parseAndHandleMessageFromAddress(buf[:length], addr); err != nil {
			klog.Errorf("Error handling message from address %s: %s:\n%s", addr.String(), err, hex.Dump(buf[:length]))
		}
	}
}

func (nd *natDiscovery) parseAndHandleMessageFromAddress(buf []byte, addr *net.UDPAddr) error {
	msg := natproto.SubmarinerNatDiscoveryMessage{}
	if err := proto.Unmarshal(buf, &msg); err != nil {
		return errors.Wrapf(err, "Error unmarshaling message received on UDP port %d", natproto.DefaultPort)
	}

	if request := msg.GetRequest(); request != nil {
		return nd.handleRequestFromAddress(request, addr)
	} else if response := msg.GetResponse(); response != nil {
		return nd.handleResponseFromAddress(response, addr)
	}

	return errors.Errorf("Message without response or request received from %#v", addr)
}
