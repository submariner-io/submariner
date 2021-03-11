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
	"net"
	"strconv"
	"time"

	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"

	natproto "github.com/submariner-io/submariner/pkg/natdiscovery/proto"
)

func (nd *natDiscovery) runListener(stopCh <-chan struct{}) error {
	serverAddress, err := net.ResolveUDPAddr("udp4", ":"+strconv.Itoa(natproto.DefaultPort))
	if err != nil {
		return errors.Wrap(err, "Error resolving UDP address")
	}

	serverConnection, err := net.ListenUDP("udp", serverAddress)
	if err != nil {
		return errors.Wrapf(err, "Error listening on udp port %d", natproto.DefaultPort)
	}

	// Instead of storing the server connection I save the reference to the WriteToUDP
	// of our server connection instance, in a way that we can use this for unit testing
	// later too.
	nd.serverUDPWrite = serverConnection.WriteToUDP

	go wait.Until(func() {
		buf := make([]byte, 2048)
		if _, addr, err := serverConnection.ReadFromUDP(buf); err != nil {
			klog.Errorf("Error receiving from udp: %s", err)
		} else if err := nd.parseAndHandleMessageFromAddress(buf, addr); err != nil {
			klog.Errorf("Error handling message from address %#v: %s", addr, err)
		}
	}, time.Second, stopCh)

	return nil
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
