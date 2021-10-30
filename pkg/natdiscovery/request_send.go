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
	natproto "github.com/submariner-io/submariner/pkg/natdiscovery/proto"
	"google.golang.org/protobuf/proto"
	"k8s.io/klog"
)

func (nd *natDiscovery) sendCheckRequest(remoteNAT *remoteEndpointNAT) error {
	var errPrivate, errPublic error
	var reqID uint64

	if remoteNAT.endpoint.Spec.PrivateIP != "" {
		reqID, errPrivate = nd.sendCheckRequestToTargetIP(remoteNAT, remoteNAT.endpoint.Spec.PrivateIP)
		if errPrivate == nil {
			remoteNAT.lastPrivateIPRequestID = reqID
		}
	}

	if remoteNAT.endpoint.Spec.PublicIP != "" {
		reqID, errPublic = nd.sendCheckRequestToTargetIP(remoteNAT, remoteNAT.endpoint.Spec.PublicIP)
		if errPublic == nil {
			remoteNAT.lastPublicIPRequestID = reqID
		}
	}

	if errPrivate != nil && errPublic != nil {
		return errors.Errorf("error while trying to discover both public & private IPs of endpoint %q, [%s, %s]",
			remoteNAT.endpoint.Spec.CableName, errPublic, errPrivate)
	}

	if errPrivate != nil {
		return errors.Wrapf(errPrivate, "error while trying to NAT-discover private IP of endpoint %q",
			remoteNAT.endpoint.Spec.CableName)
	}

	if errPublic != nil {
		return errors.Wrapf(errPublic, "error while trying to NAT-discover public IP of endpoint %q",
			remoteNAT.endpoint.Spec.CableName)
	}

	return nil
}

func (nd *natDiscovery) sendCheckRequestToTargetIP(remoteNAT *remoteEndpointNAT, targetIP string) (uint64, error) {
	targetPort, err := extractNATDiscoveryPort(&remoteNAT.endpoint.Spec)

	if err != nil {
		return 0, err
	}

	sourceIP := nd.findSrcIP(targetIP)

	nd.requestCounter++

	request := &natproto.SubmarinerNATDiscoveryRequest{
		RequestNumber: nd.requestCounter,
		Sender: &natproto.EndpointDetails{
			EndpointId: nd.localEndpoint.Spec.CableName,
			ClusterId:  nd.localEndpoint.Spec.ClusterID,
		},
		Receiver: &natproto.EndpointDetails{
			EndpointId: remoteNAT.endpoint.Spec.CableName,
			ClusterId:  remoteNAT.endpoint.Spec.ClusterID,
		},
		UsingSrc: &natproto.IPPortPair{
			IP:   sourceIP,
			Port: uint32(nd.serverPort),
		},
		UsingDst: &natproto.IPPortPair{
			IP:   targetIP,
			Port: uint32(targetPort),
		},
	}

	msgRequest := &natproto.SubmarinerNATDiscoveryMessage_Request{
		Request: request,
	}

	message := natproto.SubmarinerNATDiscoveryMessage{
		Version: natproto.Version,
		Message: msgRequest,
	}

	buf, err := proto.Marshal(&message)
	if err != nil {
		return request.RequestNumber, errors.Wrapf(err, "error marshaling request %#v", request)
	}

	addr := net.UDPAddr{
		IP:   net.ParseIP(targetIP),
		Port: int(targetPort),
	}

	klog.V(log.DEBUG).Infof("Sending request - REQUEST_NUMBER: 0x%x, SENDER: %q, RECEIVER: %q, USING_SRC: %s:%d, USING_DST: %s:%d",
		request.RequestNumber, request.Sender.EndpointId, request.Receiver.EndpointId, request.UsingSrc.IP, request.UsingSrc.Port,
		request.UsingDst.IP, request.UsingDst.Port)

	if length, err := nd.serverUDPWrite(buf, &addr); err != nil {
		return request.RequestNumber, errors.Wrapf(err, "error sending request packet %#v", request)
	} else if length != len(buf) {
		return request.RequestNumber, errors.Errorf("the sent UDP packet was smaller than requested, sent=%d, expected=%d", length,
			len(buf))
	}

	remoteNAT.checkSent()

	return request.RequestNumber, nil
}
