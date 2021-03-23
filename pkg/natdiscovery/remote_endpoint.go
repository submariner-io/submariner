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
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/types"
)

type endpointState int

const (
	testingPrivateAndPublicIPs endpointState = iota
	waitingForResponse
	selectedPublicIP
	selectedPrivateIP
)

var (
	recheckTime                    = (2 * time.Second).Nanoseconds()
	totalTimeout                   = (100000 * time.Second).Nanoseconds()
	publicToPrivateFailoverTimeout = time.Second.Nanoseconds()
)

type remoteEndpointNAT struct {
	endpoint               types.SubmarinerEndpoint
	state                  endpointState
	lastCheck              time.Time
	lastTransition         time.Time
	started                time.Time
	useNAT                 bool
	useIP                  string
	lastPublicIPRequestID  uint64
	lastPrivateIPRequestID uint64
	privateIPConnection    *net.UDPConn
	publicIPConnection     *net.UDPConn
}

type NATEndpointInfo struct {
	Endpoint types.SubmarinerEndpoint
	UseNAT   bool
	UseIP    string
}

func (rn *remoteEndpointNAT) toNATEndpointInfo() *NATEndpointInfo {
	return &NATEndpointInfo{
		Endpoint: rn.endpoint,
		UseNAT:   rn.useNAT,
		UseIP:    rn.useIP,
	}
}

type udpListenerFunction func(connection *net.UDPConn)

func newRemoteEndpointNAT(endpoint *types.SubmarinerEndpoint, listenerFunc udpListenerFunction) *remoteEndpointNAT {
	var (
		privateUDPConnection *net.UDPConn
		publicUDPConnection  *net.UDPConn
	)

	if endpoint.Spec.NATDiscoveryPort != nil {
		if endpoint.Spec.PrivateIP != "" {
			privAddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", endpoint.Spec.PrivateIP,
				int(*endpoint.Spec.NATDiscoveryPort)))
			if err != nil {
				klog.Errorf("Unable to resolve remote udp address for private IP %q on endpoint %q", endpoint.Spec.PrivateIP,
					endpoint.Spec.CableName)
			}
			privateUDPConnection, err = net.DialUDP("udp4", nil, privAddr)
			if err != nil {
				klog.Warning("Unable to create UDP connection to private IP %q for endpoint %q", endpoint.Spec.PrivateIP,
					endpoint.Spec.CableName)
			}
			go listenerFunc(privateUDPConnection)
		}

		if endpoint.Spec.PublicIP != "" {
			publicAddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", endpoint.Spec.PublicIP,
				int(*endpoint.Spec.NATDiscoveryPort)))

			publicUDPConnection, err = net.DialUDP("udp4", nil, publicAddr)
			if err != nil {
				klog.Warning("Unable to create UDP connection to public IP %q for endpoint %q", endpoint.Spec.PublicIP,
					endpoint.Spec.CableName)
			}
			go listenerFunc(publicUDPConnection)
		}
	}

	return &remoteEndpointNAT{
		endpoint:            *endpoint,
		state:               testingPrivateAndPublicIPs,
		started:             time.Now(),
		lastTransition:      time.Now(),
		publicIPConnection:  publicUDPConnection,
		privateIPConnection: privateUDPConnection,
	}
}

func (rn *remoteEndpointNAT) transitionToState(newState endpointState) {
	rn.lastTransition = time.Now()
	rn.state = newState
}

func (rn *remoteEndpointNAT) sinceLastTransition() time.Duration {
	return time.Since(rn.lastTransition)
}

func (rn *remoteEndpointNAT) hasTimedOut() bool {
	return time.Since(rn.started) > toDuration(&totalTimeout)
}

func (rn *remoteEndpointNAT) closeConnections() {
	if rn.publicIPConnection != nil {
		rn.publicIPConnection.Close()
	}
	if rn.privateIPConnection != nil {
		rn.privateIPConnection.Close()
	}
}

func (rn *remoteEndpointNAT) useLegacyNATSettings() {
	rn.useNAT = rn.endpoint.Spec.NATEnabled
	if rn.endpoint.Spec.NATEnabled {
		rn.useIP = rn.endpoint.Spec.PublicIP
		rn.transitionToState(selectedPublicIP)
		klog.V(log.DEBUG).Infof("using NAT legacy settings for endpoint %q, using public IP %q", rn.endpoint.Spec.CableName,
			rn.useIP)
	} else {
		rn.useIP = rn.endpoint.Spec.PrivateIP
		rn.transitionToState(selectedPrivateIP)
		klog.V(log.DEBUG).Infof("using NAT legacy settings for endpoint %q, using private IP %q", rn.endpoint.Spec.CableName,
			rn.useIP)
	}
	rn.closeConnections()
}

func (rn *remoteEndpointNAT) shouldCheck() bool {
	switch rn.state {
	case testingPrivateAndPublicIPs:
		return true
	case waitingForResponse:
		return time.Since(rn.lastCheck) > toDuration(&recheckTime)
	default:
		return false
	}
}

func (rn *remoteEndpointNAT) checkSent() {
	if rn.state == testingPrivateAndPublicIPs {
		rn.state = waitingForResponse
	}

	rn.lastCheck = time.Now()
}

func (rn *remoteEndpointNAT) transitionToPublicIP(remoteEndpointID string, useNAT bool) error {
	if rn.state == waitingForResponse {
		rn.useIP = rn.endpoint.Spec.PublicIP
		rn.useNAT = useNAT
		rn.transitionToState(selectedPublicIP)
		klog.V(log.DEBUG).Infof("selected public IP %q for endpoint %q", rn.useIP, rn.endpoint.Spec.CableName)
	} else {
		return errors.Errorf("received unexpected transition to public IP from endpoint %q", remoteEndpointID)
	}

	return nil
}

func (rn *remoteEndpointNAT) transitionToPrivateIP(remoteEndpointID string, useNAT bool) error {
	switch rn.state {
	case waitingForResponse:
		rn.useIP = rn.endpoint.Spec.PrivateIP
		rn.useNAT = useNAT
		rn.transitionToState(selectedPrivateIP)
		klog.V(log.DEBUG).Infof("selected private IP %q for endpoint %q", rn.useIP, rn.endpoint.Spec.CableName)

	case selectedPublicIP:
		// If a PublicIP was selected, we still allow some time for the privateIP response to arrive, and we always
		// prefer PrivateIP with no NAT connection, as it will be more likely to work, and more efficient
		if rn.sinceLastTransition() > toDuration(&publicToPrivateFailoverTimeout) {
			return errors.Errorf("response on private address received too late after response on public address for endpoint %q",
				remoteEndpointID)
		}

		rn.useIP = rn.endpoint.Spec.PrivateIP
		rn.useNAT = useNAT
		rn.transitionToState(selectedPrivateIP)
		klog.V(log.DEBUG).Infof("updated to private IP %q for endpoint %q", rn.useIP, rn.endpoint.Spec.CableName)

	default:
		return errors.Errorf("received unexpected transition to private IP from endpoint %q", remoteEndpointID)
	}

	return nil
}

func toDuration(v *int64) time.Duration {
	return time.Duration(atomic.LoadInt64(v))
}
