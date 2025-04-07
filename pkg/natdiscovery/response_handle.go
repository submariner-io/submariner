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
	"fmt"
	"net"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/natdiscovery/proto"
)

func (nd *natDiscovery) handleResponseFromAddress(req *proto.SubmarinerNATDiscoveryResponse, addr *net.UDPAddr) error {
	logger.V(log.DEBUG).Infof("Received response from %s:%d - REQUEST_NUMBER: 0x%x, RESPONSE: %v, SENDER: %q, RECEIVER: %q",
		addr.IP.String(), addr.Port, req.GetRequestNumber(), req.GetResponse(), req.GetSender().GetEndpointId(),
		req.GetReceiver().GetEndpointId())

	if req.GetSender() == nil || req.GetReceiver() == nil || req.GetReceivedSrc() == nil {
		return errors.Errorf("received malformed response %#v", req)
	}

	if req.GetResponse() != proto.ResponseType_OK && req.GetResponse() != proto.ResponseType_NAT_DETECTED {
		var ok bool
		var name string

		if name, ok = proto.ResponseType_name[int32(req.GetResponse())]; !ok {
			name = fmt.Sprintf("%d", req.GetResponse())
		}

		return errors.Errorf("remote endpoint %q responded with %q : %#v", req.GetSender().GetEndpointId(), name, req)
	}

	nd.Lock()
	remoteNAT, ok := nd.remoteEndpoints[req.GetSender().GetEndpointId()]
	defer nd.Unlock()

	if !ok {
		return errors.Errorf("received response from unknown endpoint %q", req.GetSender().GetEndpointId())
	}

	// response to a PublicIP request
	if remoteNAT.lastPublicIPRequestID == req.GetRequestNumber() {
		useNAT := req.GetResponse() == proto.ResponseType_NAT_DETECTED
		if !remoteNAT.transitionToPublicIP(req.GetSender().GetEndpointId(), useNAT) {
			return nil
		}

		nd.readyChannel <- remoteNAT.toNATEndpointInfo()

		return nil
	}

	// response to a PrivateIP request
	if remoteNAT.lastPrivateIPRequestID == req.GetRequestNumber() {
		if addr.IP.String() != remoteNAT.endpoint.Spec.GetPrivateIP(remoteNAT.family) {
			return errors.Errorf("response for NAT discovery on endpoint %q private IP %q comes from different IP %q, "+
				"NAT on private IPs is unlikely and filtered for security reasons",
				req.GetSender().GetEndpointId(), remoteNAT.endpoint.Spec.GetPrivateIP(remoteNAT.family), addr.IP)
		}

		if req.GetResponse() == proto.ResponseType_NAT_DETECTED {
			logger.Warningf("response for NAT discovery on endpoint %q private IP %q says src was modified which is unexpected",
				req.GetSender().GetEndpointId(), remoteNAT.endpoint.Spec.GetPrivateIP(remoteNAT.family))
		}

		useNAT := req.GetResponse() == proto.ResponseType_NAT_DETECTED

		if !remoteNAT.transitionToPrivateIP(req.GetSender().GetEndpointId(), useNAT) {
			return nil
		}

		nd.readyChannel <- remoteNAT.toNATEndpointInfo()

		return nil
	}

	return errors.Errorf("received response for unknown request id 0x%x, lastPublicIPRequestID: %d, lastPrivateIPRequestID: %d",
		req.GetRequestNumber(), remoteNAT.lastPublicIPRequestID, remoteNAT.lastPrivateIPRequestID)
}
