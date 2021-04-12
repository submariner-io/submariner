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
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"k8s.io/klog"
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
	totalTimeout                   = (60 * time.Second).Nanoseconds()
	publicToPrivateFailoverTimeout = time.Second.Nanoseconds()
)

type remoteEndpointNAT struct {
	endpoint               v1.Endpoint
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
	Endpoint v1.Endpoint
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

func newRemoteEndpointNAT(endpoint *v1.Endpoint) *remoteEndpointNAT {
	return &remoteEndpointNAT{
		endpoint:       *endpoint,
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
	return time.Since(rn.started) > toDuration(&totalTimeout)
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
}

func (rn *remoteEndpointNAT) isDiscoveryComplete() bool {
	return rn.state == selectedPublicIP || rn.state == selectedPrivateIP
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

func (rn *remoteEndpointNAT) transitionToPublicIP(remoteEndpointID string, useNAT bool) bool {
	switch rn.state {
	case waitingForResponse:
		rn.useIP = rn.endpoint.Spec.PublicIP
		rn.useNAT = useNAT
		rn.transitionToState(selectedPublicIP)
		klog.V(log.DEBUG).Infof("selected public IP %q for endpoint %q", rn.useIP, rn.endpoint.Spec.CableName)

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
	case waitingForResponse:
		rn.useIP = rn.endpoint.Spec.PrivateIP
		rn.useNAT = useNAT
		rn.transitionToState(selectedPrivateIP)
		klog.V(log.DEBUG).Infof("selected private IP %q for endpoint %q", rn.useIP, rn.endpoint.Spec.CableName)

		return true
	case selectedPublicIP:
		// If a PublicIP was selected, we still allow some time for the privateIP response to arrive, and we always
		// prefer PrivateIP with no NAT connection, as it will be more likely to work, and more efficient
		if rn.sinceLastTransition() > toDuration(&publicToPrivateFailoverTimeout) {
			klog.V(log.DEBUG).Infof("Response on private IP received too late after response on public IP for endpoint %q",
				remoteEndpointID)
			return false
		}

		rn.useIP = rn.endpoint.Spec.PrivateIP
		rn.useNAT = useNAT
		rn.transitionToState(selectedPrivateIP)
		klog.V(log.DEBUG).Infof("updated to private IP %q for endpoint %q", rn.useIP, rn.endpoint.Spec.CableName)

		return true
	default:
		klog.Errorf("Received unexpected transition from %v to private IP for endpoint %q", rn.state, remoteEndpointID)
		return false
	}
}

func toDuration(v *int64) time.Duration {
	return time.Duration(atomic.LoadInt64(v))
}
