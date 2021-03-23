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
	"net"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"google.golang.org/protobuf/proto"
	"k8s.io/klog"

	natproto "github.com/submariner-io/submariner/pkg/natdiscovery/proto"
)

func (nd *natDiscovery) sendCheckRequest(remoteNAT *remoteEndpointNAT) error {
	var errPrivate, errPublic error
	var reqID uint64

	klog.V(log.DEBUG).Infof("NAT sending check request to endpoint %q on ips: %q and %q", remoteNAT.endpoint.Spec.CableName,
		remoteNAT.endpoint.Spec.PrivateIP, remoteNAT.endpoint.Spec.PublicIP)

	if remoteNAT.privateIPConnection != nil {
		reqID, errPrivate = nd.sendCheckRequestToTargetIP(remoteNAT, remoteNAT.privateIPConnection)
		if errPrivate == nil {
			remoteNAT.lastPrivateIPRequestID = reqID
		}
	}

	if remoteNAT.publicIPConnection != nil {
		reqID, errPublic = nd.sendCheckRequestToTargetIP(remoteNAT, remoteNAT.publicIPConnection)
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

func (nd *natDiscovery) sendCheckRequestToTargetIP(remoteNAT *remoteEndpointNAT, connection *net.UDPConn) (uint64, error) {

	localAddr := connection.LocalAddr().(*net.UDPAddr)
	remoteAddr := connection.RemoteAddr().(*net.UDPAddr)

	nd.requestCounter++

	request := &natproto.SubmarinerNatDiscoveryRequest{
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
			IP:   localAddr.IP.String(),
			Port: uint32(localAddr.Port),
		},
		UsingDst: &natproto.IPPortPair{
			IP:   remoteAddr.IP.String(),
			Port: uint32(remoteAddr.Port),
		},
	}

	msgRequest := &natproto.SubmarinerNatDiscoveryMessage_Request{
		Request: request,
	}

	message := natproto.SubmarinerNatDiscoveryMessage{
		Version: natproto.Version,
		Message: msgRequest,
	}

	buf, err := proto.Marshal(&message)
	if err != nil {
		return request.RequestNumber, errors.Wrapf(err, "error marshaling request %#v", request)
	}

	klog.V(log.DEBUG).Infof("Sending request - REQUEST_NUMBER: 0x%x, SENDER: %q, RECEIVER: %q, USING_SRC: %s:%d, USING_DST: %s:%d",
		request.RequestNumber, request.Sender.EndpointId, request.Receiver.EndpointId, request.UsingSrc.IP, request.UsingSrc.Port,
		request.UsingDst.IP, request.UsingDst.Port)

	if length, err := connection.Write(buf); err != nil {
		return request.RequestNumber, errors.Wrapf(err, "error sending request packet %#v", request)
	} else if length != len(buf) {
		return request.RequestNumber, errors.Errorf("the sent UDP packet was smaller than requested, sent=%d, expected=%d", length,
			len(buf))
	}

	remoteNAT.checkSent()

	return request.RequestNumber, nil
}
