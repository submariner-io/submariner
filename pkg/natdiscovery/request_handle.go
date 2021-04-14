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

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	proto2 "google.golang.org/protobuf/proto"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/natdiscovery/proto"
)

func (nd *natDiscovery) handleRequestFromAddress(req *proto.SubmarinerNatDiscoveryRequest, addr *net.UDPAddr) error {
	response := proto.SubmarinerNatDiscoveryResponse{
		RequestNumber: req.RequestNumber,
		Sender: &proto.EndpointDetails{
			ClusterId:  nd.localEndpoint.Spec.ClusterID,
			EndpointId: nd.localEndpoint.Spec.CableName,
		},
		Receiver: req.Sender,
		ReceivedSrc: &proto.IPPortPair{
			Port: uint32(addr.Port),
			IP:   addr.IP.String(),
		},
	}

	if req.Receiver == nil || req.Sender == nil || req.UsingDst == nil || req.UsingSrc == nil {
		klog.Warningf("Received NAT discovery packet %#v from %s which seems to be malformed ", req, addr.String())

		response.Response = proto.ResponseType_MALFORMED

		return nd.sendResponseToAddress(&response, addr)
	}

	klog.V(log.DEBUG).Infof("Received request from %s:%d - REQUEST_NUMBER: 0x%x, SENDER: %q, RECEIVER: %q",
		addr.IP.String(), addr.Port, req.RequestNumber, req.Sender.EndpointId, req.Receiver.EndpointId)

	if req.Receiver.GetClusterId() != nd.localEndpoint.Spec.ClusterID {
		klog.Warningf("Received NAT discovery packet for cluster %q, but we are cluster %q", req.Receiver.GetClusterId(),
			nd.localEndpoint.Spec.ClusterID)

		response.Response = proto.ResponseType_UNKNOWN_DST_CLUSTER

		return nd.sendResponseToAddress(&response, addr)
	}

	if req.Receiver.GetEndpointId() != nd.localEndpoint.Spec.CableName {
		klog.Warningf("Received NAT discovery packet for endpoint %q, but we are endpoint %q "+
			"if the port for NAT discovery has been mapped somewhere an error may exist", req.Receiver.GetEndpointId(),
			nd.localEndpoint.Spec.CableName)

		response.Response = proto.ResponseType_UNKNOWN_DST_ENDPOINT

		return nd.sendResponseToAddress(&response, addr)
	}

	if req.UsingSrc.GetIP() != "" && req.UsingSrc.GetIP() != addr.IP.String() {
		klog.V(log.DEBUG).Infof("Received NAT packet from endpoint %q, cluster %q, where NAT has been detected, "+
			"source IP changed",
			req.Sender.GetEndpointId(), req.Sender.GetClusterId())
		klog.V(log.DEBUG).Infof("Original src IP was %q, received src IP is %q", req.UsingSrc.IP, addr.IP.String())

		response.Response = proto.ResponseType_SRC_MODIFIED

		return nd.sendResponseToAddress(&response, addr)
	}

	if int(req.UsingSrc.Port) != addr.Port {
		klog.V(log.DEBUG).Infof("Received NAT packet from endpoint %q, cluster %q, where NAT on the source has been detected, "+
			"src port changed",
			req.Sender.GetEndpointId(), req.Sender.GetClusterId())
		klog.V(log.DEBUG).Infof("Original src IP was %q, received src IP is %q", req.UsingSrc.IP, addr.IP.String())

		response.Response = proto.ResponseType_SRC_MODIFIED

		return nd.sendResponseToAddress(&response, addr)
	}

	response.Response = proto.ResponseType_OK

	return nd.sendResponseToAddress(&response, addr)
}

func (nd *natDiscovery) sendResponseToAddress(response *proto.SubmarinerNatDiscoveryResponse, addr *net.UDPAddr) error {
	msgResponse := proto.SubmarinerNatDiscoveryMessage_Response{Response: response}
	message := proto.SubmarinerNatDiscoveryMessage{Message: &msgResponse}
	buf, err := proto2.Marshal(&message)
	if err != nil {
		return errors.Wrapf(err, "error marshaling response %#v", response)
	}

	klog.V(log.DEBUG).Infof("Sending response to %s:%d - REQUEST_NUMBER: 0x%x, RESPONSE: %v, SENDER: %q, RECEIVER: %q",
		addr.IP.String(), addr.Port, response.RequestNumber, response.Response, response.GetSenderEndpointID(),
		response.GetReceiverEndpointID())

	if length, err := nd.serverUDPWrite(buf, addr); err != nil {
		return errors.Wrapf(err, "error sending response packet %#v", response)
	} else if length != len(buf) {
		return errors.Errorf("the sent UDP packet was smaller than requested, sent=%d, expected=%d", length, len(buf))
	}

	return nil
}
