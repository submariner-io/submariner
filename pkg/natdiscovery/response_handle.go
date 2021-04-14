/*
© 2021 Red Hat, Inc. and others

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
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/natdiscovery/proto"
)

func (nd *natDiscovery) handleResponseFromAddress(req *proto.SubmarinerNatDiscoveryResponse, addr *net.UDPAddr) error {
	klog.V(log.DEBUG).Infof("Received response from %s:%d - REQUEST_NUMBER: 0x%x, RESPONSE: %v, SENDER: %q, RECEIVER: %q",
		addr.IP.String(), addr.Port, req.RequestNumber, req.Response, req.Sender.EndpointId, req.Receiver.EndpointId)

	if req.GetSender() == nil || req.GetReceiver() == nil || req.GetReceivedSrc() == nil {
		return errors.Errorf("received malformed response %#v", req)
	}

	if req.Response != proto.ResponseType_OK && req.Response != proto.ResponseType_SRC_MODIFIED {
		var ok bool
		var name string

		if name, ok = proto.ResponseType_name[int32(req.Response)]; !ok {
			name = fmt.Sprintf("%d", req.Response)
		}

		return errors.Errorf("remote endpoint %q responded with %q : %#v", req.Sender.EndpointId, name, req)
	}

	nd.Lock()
	remoteNat, ok := nd.remoteEndpoints[req.GetSender().EndpointId]
	defer nd.Unlock()

	if !ok {
		return errors.Errorf("received response from unknown endpoint %q", req.GetSender().EndpointId)
	}

	// response to a PublicIP request
	if remoteNat.lastPublicIPRequestID == req.RequestNumber {
		useNAT := req.Response == proto.ResponseType_SRC_MODIFIED
		if !remoteNat.transitionToPublicIP(req.GetSender().EndpointId, useNAT) {
			return nil
		}

		nd.readyChannel <- remoteNat.toNATEndpointInfo()

		return nil
	}

	// response to a PrivateIP request
	if remoteNat.lastPrivateIPRequestID == req.RequestNumber {
		if addr.IP.String() != remoteNat.endpoint.Spec.PrivateIP {
			return errors.Errorf("response for NAT discovery on endpoint %q private IP %q comes from different IP %q, "+
				"NAT on private IPs is unlikely and filtered for security reasons",
				req.GetSender().EndpointId, remoteNat.endpoint.Spec.PrivateIP, addr.IP)
		}

		if req.Response == proto.ResponseType_SRC_MODIFIED {
			klog.Warningf("response for NAT discovery on endpoint %q private IP %q says src was modified which is unexpected",
				req.GetSender().EndpointId, remoteNat.endpoint.Spec.PrivateIP)
		}

		useNAT := req.Response == proto.ResponseType_SRC_MODIFIED

		if !remoteNat.transitionToPrivateIP(req.GetSender().EndpointId, useNAT) {
			return nil
		}

		nd.readyChannel <- remoteNat.toNATEndpointInfo()

		return nil
	}

	return errors.Errorf("received response for unknown request id 0x%x, lastPublicIPRequestID: %d, lastPrivateIPRequestID: %d",
		req.RequestNumber, remoteNat.lastPublicIPRequestID, remoteNat.lastPrivateIPRequestID)
}
