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
	"time"

	"github.com/pkg/errors"

	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/types"
)

type endpointState int

const (
	testingPrivateAndPublicIPs endpointState = iota
	waitingForResponse
	selectedPublicIP
	selectedPrivateIP
	recheckTime                    = 2 * time.Second
	totalTimeout                   = 60 * time.Second
	publicToPrivateFailoverTimeout = 1 * time.Second
)

type remoteEndpointNAT struct {
	endpoint               *types.SubmarinerEndpoint
	state                  endpointState
	lastCheck              time.Time
	lastTransition         time.Time
	started                time.Time
	useNAT                 bool
	useIP                  string
	lastPublicIPRequestID  uint64
	lastPrivateIPRequestID uint64
}

type NATEndpointInfo struct {
	Endpoint subv1.EndpointSpec
	UseNAT   bool
	UseIP    string
}

func (rn *remoteEndpointNAT) toNATEndpointInfo() *NATEndpointInfo {
	return &NATEndpointInfo{
		Endpoint: rn.endpoint.Spec,
		UseNAT:   rn.useNAT,
		UseIP:    rn.useIP,
	}
}

func newRemoteEndpointNAT(endpoint *types.SubmarinerEndpoint) *remoteEndpointNAT {
	return &remoteEndpointNAT{
		endpoint:       endpoint,
		state:          testingPrivateAndPublicIPs,
		started:        time.Now(),
		lastTransition: time.Now(),
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
	return time.Since(rn.started) > totalTimeout
}

func (rn *remoteEndpointNAT) useLegacyNATSettings() {
	rn.useNAT = rn.endpoint.Spec.NATEnabled
	if rn.endpoint.Spec.NATEnabled {
		rn.useIP = rn.endpoint.Spec.PublicIP
		rn.transitionToState(selectedPublicIP)
	} else {
		rn.useIP = rn.endpoint.Spec.PrivateIP
		rn.transitionToState(selectedPrivateIP)
	}
}

func (rn *remoteEndpointNAT) shouldCheck() bool {
	switch rn.state {
	case testingPrivateAndPublicIPs:
		return true
	case waitingForResponse:
		return time.Since(rn.lastCheck) > recheckTime
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

	case selectedPublicIP:
		// If a PublicIP was selected, we still allow some time for the privateIP response to arrive, and we always
		// prefer PrivateIP with no NAT connection, as it will be more likely to work, and more efficient
		if rn.sinceLastTransition() > publicToPrivateFailoverTimeout {
			return errors.Errorf("response on private address received too late after response on public address for endpoint %q",
				remoteEndpointID)
		}

		rn.useIP = rn.endpoint.Spec.PrivateIP
		rn.useNAT = useNAT
		rn.transitionToState(selectedPrivateIP)

	default:
		return errors.Errorf("received unexpected transition to private IP from endpoint %q", remoteEndpointID)
	}

	return nil
}
