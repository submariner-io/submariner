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

package natdiscovery

import (
	"encoding/hex"
	"net"
	"strconv"

	"github.com/pkg/errors"
	natproto "github.com/submariner-io/submariner/pkg/natdiscovery/proto"
	"google.golang.org/protobuf/proto"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	k8snet "k8s.io/utils/net"
)

type ServerConnection interface {
	Close() error
	ReadFromUDP(b []byte) (int, *net.UDPAddr, error)
	WriteToUDP(b []byte, addr *net.UDPAddr) (int, error)
}

func (nd *natDiscovery) runListeners(stopCh <-chan struct{}) error {
	for _, family := range nd.localEndpoint.Spec().GetIPFamilies() {
		if err := nd.runListener(family, stopCh); err != nil {
			return err
		}

		logger.Infof("NAT discovery started listener for IPv%v", family)
	}

	return nil
}

func (nd *natDiscovery) runListener(family k8snet.IPFamily, stopCh <-chan struct{}) error {
	var errs []error

	if family == k8snet.IPv4 {
		err := nd.runListenerV4(stopCh)
		if err != nil {
			logger.Errorf(err, "Error running IPv%v listener", family)
			errs = append(errs, err)
		}
	}

	// TODO_IPV6: add V6 runListener for V6
	return utilerrors.NewAggregate(errs)
}

func (nd *natDiscovery) runListenerV4(stopCh <-chan struct{}) error {
	if nd.serverPort == 0 {
		logger.Infof("NAT discovery protocol port not set for this gateway")
		return nil
	}

	serverConnection, err := nd.createServerConnection(nd.serverPort, k8snet.IPv4)
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

func createServerConnection(port int32, _ k8snet.IPFamily) (ServerConnection, error) {
	network := "udp4"

	serverAddress, err := net.ResolveUDPAddr(network, ":"+strconv.Itoa(int(port)))
	if err != nil {
		return nil, errors.Wrap(err, "Error resolving UDP address")
	}

	serverConnection, err := net.ListenUDP(network, serverAddress)
	if err != nil {
		return nil, errors.Wrapf(err, "Error listening on udp port %d", port)
	}

	return serverConnection, nil
}

func (nd *natDiscovery) listenerLoop(serverConnection ServerConnection) {
	buf := make([]byte, 2048)

	for {
		length, addr, err := serverConnection.ReadFromUDP(buf)
		if length == 0 {
			logger.Info("Stopping NAT listener")
			return
		} else if err != nil {
			logger.Errorf(err, "Error receiving from udp")
		} else if err := nd.parseAndHandleMessageFromAddress(buf[:length], addr); err != nil {
			logger.Errorf(err, "Error handling message from address %s:\n%s", addr.String(), hex.Dump(buf[:length]))
		}
	}
}

func (nd *natDiscovery) parseAndHandleMessageFromAddress(buf []byte, addr *net.UDPAddr) error {
	msg := natproto.SubmarinerNATDiscoveryMessage{}
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
