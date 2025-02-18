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
	"net"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/natdiscovery/proto"
	proto2 "google.golang.org/protobuf/proto"
	k8snet "k8s.io/utils/net"
)

func (nd *natDiscovery) handleRequestFromAddress(req *proto.SubmarinerNATDiscoveryRequest, addr *net.UDPAddr) error {
	localEndpointSpec := nd.localEndpoint.Spec()

	family := k8snet.IPv4
	if addr.IP.To4() == nil {
		family = k8snet.IPv6
	}

	response := proto.SubmarinerNATDiscoveryResponse{
		RequestNumber: req.GetRequestNumber(),
		Sender: &proto.EndpointDetails{
			ClusterId:  localEndpointSpec.ClusterID,
			EndpointId: localEndpointSpec.GetFamilyCableName(family),
		},
		Receiver: req.GetSender(),
		ReceivedSrc: &proto.IPPortPair{
			Port: int32(addr.Port), //nolint:gosec // We can safely ignore integer conversion error
			IP:   addr.IP.String(),
		},
	}

	if req.GetReceiver() == nil || req.GetSender() == nil || req.GetUsingDst() == nil || req.GetUsingSrc() == nil {
		logger.Warningf("Received NAT discovery packet %#v from %s which seems to be malformed ", req, addr.String())

		response.Response = proto.ResponseType_MALFORMED

		return nd.sendResponseToAddress(&response, addr)
	}

	logger.V(log.DEBUG).Infof("Received request from %s:%d - REQUEST_NUMBER: 0x%x, SENDER: %q, RECEIVER: %q",
		addr.IP.String(), addr.Port, req.GetRequestNumber(), req.GetSender().GetEndpointId(), req.GetReceiver().GetEndpointId())

	if req.GetReceiver().GetClusterId() != localEndpointSpec.ClusterID {
		logger.Warningf("Received NAT discovery packet for cluster %q, but we are cluster %q", req.GetReceiver().GetClusterId(),
			localEndpointSpec.ClusterID)

		response.Response = proto.ResponseType_UNKNOWN_DST_CLUSTER

		return nd.sendResponseToAddress(&response, addr)
	}

	if req.GetReceiver().GetEndpointId() != localEndpointSpec.GetFamilyCableName(family) {
		logger.Warningf("Received NAT discovery packet for endpoint %q, but we are endpoint %q "+
			"if the port for NAT discovery has been mapped somewhere an error may exist", req.GetReceiver().GetEndpointId(),
			localEndpointSpec.CableName)

		response.Response = proto.ResponseType_UNKNOWN_DST_ENDPOINT

		return nd.sendResponseToAddress(&response, addr)
	}

	if req.GetUsingSrc().GetIP() != "" && req.GetUsingSrc().GetIP() != addr.IP.String() {
		logger.V(log.DEBUG).Infof("Received NAT packet from endpoint %q, cluster %q, where NAT has been detected, "+
			"source IP changed",
			req.GetSender().GetEndpointId(), req.GetSender().GetClusterId())
		logger.V(log.DEBUG).Infof("Original src IP was %q, received src IP is %q", req.GetUsingSrc().GetIP(), addr.IP.String())

		response.SrcIpNatDetected = true
	}

	if int(req.GetUsingSrc().GetPort()) != addr.Port {
		logger.V(log.DEBUG).Infof("Received NAT packet from endpoint %q, cluster %q, where NAT on the source has been detected, "+
			"src port changed",
			req.GetSender().GetEndpointId(), req.GetSender().GetClusterId())
		logger.V(log.DEBUG).Infof("Original src IP was %q, received src IP is %q", req.GetUsingSrc().GetIP(), addr.IP.String())

		response.SrcPortNatDetected = true
	}

	// Detect DST NAT with a naive implementation that assumes that we always receive on the PrivateIP,
	// if we will listen at some point on multiple addresses we will need to implement the
	// unix.IP_RECVORIGDSTADDR on the UDP socket, and the go recvmsg implementation instead of readfrom
	if req.GetUsingDst().GetIP() != localEndpointSpec.GetPrivateIP(k8snet.IPv4) {
		response.DstIpNatDetected = true
	}

	if response.GetSrcPortNatDetected() || response.GetSrcIpNatDetected() || response.GetDstIpNatDetected() {
		response.Response = proto.ResponseType_NAT_DETECTED
	} else {
		response.Response = proto.ResponseType_OK
	}

	return nd.sendResponseToAddress(&response, addr)
}

func (nd *natDiscovery) sendResponseToAddress(response *proto.SubmarinerNATDiscoveryResponse, addr *net.UDPAddr) error {
	msgResponse := proto.SubmarinerNATDiscoveryMessage_Response{Response: response}
	message := proto.SubmarinerNATDiscoveryMessage{Message: &msgResponse}

	buf, err := proto2.Marshal(&message)
	if err != nil {
		return errors.Wrapf(err, "error marshaling response %#v", response)
	}

	logger.V(log.DEBUG).Infof("Sending response to %s:%d - REQUEST_NUMBER: 0x%x, RESPONSE: %v, SENDER: %q, RECEIVER: %q",
		addr.IP.String(), addr.Port, response.GetRequestNumber(), response.GetResponse(), response.GetSenderEndpointID(),
		response.GetReceiverEndpointID())

	if length, err := nd.serverUDPWrite(buf, addr); err != nil {
		return errors.Wrapf(err, "error sending response packet %#v", response)
	} else if length != len(buf) {
		return errors.Errorf("the sent UDP packet was smaller than requested, sent=%d, expected=%d", length, len(buf))
	}

	return nil
}
