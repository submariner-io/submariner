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
	"sync/atomic"
	"time"

	"github.com/submariner-io/admiral/pkg/log"
	"k8s.io/klog"

	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/types"
)

type endpointState int

const (
	testingPrivateAndPublicIPs endpointState = iota
	waitingForResponse
	selectedPublicIP
	selectedPrivateIP
	selectPublicIPPending
)

var (
	recheckTime  = (2 * time.Second).Nanoseconds()
	totalTimeout = (60 * time.Second).Nanoseconds()

	// If a public IP was selected, we still allow some time for the private IP response to arrive - we always
	// prefer the private IP with no NAT connection as it will be more likely to work and more efficiently.
	publicToPrivateGracePeriod = time.Second.Nanoseconds()
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
	readyChannel           chan *NATEndpointInfo
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

func newRemoteEndpointNAT(endpoint *types.SubmarinerEndpoint, readyChannel chan *NATEndpointInfo) *remoteEndpointNAT {
	return &remoteEndpointNAT{
		endpoint:       *endpoint,
		state:          testingPrivateAndPublicIPs,
		started:        time.Now(),
		lastTransition: time.Now(),
		readyChannel:   readyChannel,
	}
}

func (rn *remoteEndpointNAT) transitionToState(newState endpointState) {
	if rn.state == newState {
		return
	}

	rn.lastTransition = time.Now()
	rn.state = newState

	if rn.state == selectedPublicIP || rn.state == selectedPrivateIP {
		rn.sendReady()
	}
}

func (rn *remoteEndpointNAT) sinceLastTransition() time.Duration {
	return time.Since(rn.lastTransition)
}

func (rn *remoteEndpointNAT) hasTimedOut() bool {
	return time.Since(rn.started) > toDuration(&totalTimeout)
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
		return time.Since(rn.lastCheck) > toDuration(&recheckTime)
	case selectPublicIPPending:
		if rn.sinceLastTransition() > toDuration(&publicToPrivateGracePeriod) {
			klog.V(log.DEBUG).Infof("Response for private IP received within grace period after public IP response for endpoint %q",
				rn.endpoint.Spec.CableName)
			rn.transitionToState(selectedPublicIP)
		}

		return false
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

func (rn *remoteEndpointNAT) transitionToPublicIP(remoteEndpointID string, useNAT bool) bool {
	switch rn.state {
	case waitingForResponse:
		rn.useIP = rn.endpoint.Spec.PublicIP
		rn.useNAT = useNAT

		if rn.endpoint.Spec.PrivateIP == "" {
			rn.transitionToState(selectedPublicIP)
		} else {
			rn.transitionToState(selectPublicIPPending)
		}

		return true
	case selectedPrivateIP:
		return false
	default:
		klog.Errorf("Received unexpected transition from %v to public IP for endpoint %q", rn.state, remoteEndpointID)
		return false
	}
}

func (rn *remoteEndpointNAT) transitionToPrivateIP(remoteEndpointID string, useNAT bool) bool {
	switch rn.state {
	case waitingForResponse, selectPublicIPPending:
		rn.useIP = rn.endpoint.Spec.PrivateIP
		rn.useNAT = useNAT
		rn.transitionToState(selectedPrivateIP)

		return true
	case selectedPublicIP:
		return false
	default:
		klog.Errorf("Received unexpected transition from %v to private IP for endpoint %q", rn.state, remoteEndpointID)
		return false
	}
}

func (rn *remoteEndpointNAT) sendReady() {
	if rn.readyChannel != nil {
		rn.readyChannel <- rn.toNATEndpointInfo()
	}
}

func toDuration(v *int64) time.Duration {
	return time.Duration(atomic.LoadInt64(v))
}

func (e endpointState) String() string {
	switch e {
	case testingPrivateAndPublicIPs:
		return "testingPrivateAndPublicIPs"
	case waitingForResponse:
		return "waitingForResponse"
	case selectedPublicIP:
		return "selectedPublicIP"
	case selectedPrivateIP:
		return "selectedPrivateIP"
	case selectPublicIPPending:
		return "selectPublicIPPending"
	default:
		return "unknown"
	}
}
