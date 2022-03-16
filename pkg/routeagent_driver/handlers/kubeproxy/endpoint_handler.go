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

package kubeproxy

import (
	"net"

	"github.com/pkg/errors"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cidr"
	"k8s.io/klog"
)

func (kp *SyncHandler) LocalEndpointCreated(endpoint *submV1.Endpoint) error {
	kp.syncHandlerMutex.Lock()
	defer kp.syncHandlerMutex.Unlock()
	kp.localCableDriver = endpoint.Spec.Backend

	// We are on nonGateway node
	if endpoint.Spec.Hostname != kp.hostname {
		// If the node already has a vxLAN interface that points to an oldEndpoint
		// (i.e., during gateway migration), delete it.
		if kp.vxlanDevice != nil && kp.vxlanDevice.activeEndpointHostname != endpoint.Spec.Hostname {
			err := kp.vxlanDevice.deleteVxLanIface()
			if err != nil {
				return errors.Wrapf(err, "failed to delete the the vxlan interface that points to old endpoint %s",
					kp.vxlanDevice.activeEndpointHostname)
			}

			kp.vxlanDevice = nil
		}

		kp.isGatewayNode = false
		localClusterGwNodeIP := net.ParseIP(endpoint.Spec.PrivateIP)

		klog.Infof("Creating the vxlan interface %s with gateway node IP %s", VxLANIface, localClusterGwNodeIP)

		kp.gwIPs.Add(localClusterGwNodeIP.String())

		err := kp.createVxLANInterface(endpoint.Spec.Hostname, VxInterfaceWorker)
		if err != nil {
			klog.Fatalf("Unable to create VxLAN interface on non-GatewayNode (%s): %v", endpoint.Spec.Hostname, err)
		}

		err = kp.reconcileRoutes(localClusterGwNodeIP)
		if err != nil {
			return errors.Wrap(err, "error while reconciling routes")
		}
	}

	return nil
}

func (kp *SyncHandler) LocalEndpointUpdated(endpoint *submV1.Endpoint) error {
	return nil
}

func (kp *SyncHandler) LocalEndpointRemoved(endpoint *submV1.Endpoint) error {
	kp.syncHandlerMutex.Lock()
	defer kp.syncHandlerMutex.Unlock()
	kp.isGatewayNode = false

	localClusterGwNodeIP := net.ParseIP(endpoint.Spec.PrivateIP)

	// If a local gateway was removed, make sure we remove the FDB entry directing traffic to it
	if kp.vxlanDevice != nil && kp.vxlanDevice.activeEndpointHostname == endpoint.Spec.Hostname {
		kp.gwIPs.Remove(localClusterGwNodeIP.String())

		// If a local gateway was removed, and there are no other gateways, cleanup the vxlan interface
		if kp.gwIPs.Size() == 0 {
			err := kp.vxlanDevice.deleteVxLanIface()
			kp.vxlanDevice = nil
			if err != nil {
				return errors.Wrap(err, "failed to delete the the vxlan interface on Endpoint removal")
			}
			// exit early if device is gone
			return nil
		}

		// Otherwise update cluster Local FDP entries for interface

		kp.vxlanDevice.DelFDB(localClusterGwNodeIP, "00:00:00:00:00:00")

	}

	return nil
}

func (kp *SyncHandler) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	if err := cidr.OverlappingSubnets(kp.localServiceCidr, kp.localClusterCidr, endpoint.Spec.Subnets); err != nil {
		// Skip processing the endpoint when CIDRs overlap and return nil to avoid re-queuing.
		klog.Errorf("overlappingSubnets for new remote %#v returned error: %v", endpoint, err)
		return nil
	}

	kp.syncHandlerMutex.Lock()
	defer kp.syncHandlerMutex.Unlock()

	lastProcessedTime, ok := kp.remoteEndpointTimeStamp[endpoint.Spec.ClusterID]

	if ok && lastProcessedTime.After(endpoint.CreationTimestamp.Time) {
		klog.Infof("Ignoring new remote %#v since a later endpoint was already"+
			"processed", endpoint)
		return nil
	}

	for _, inputCidrBlock := range endpoint.Spec.Subnets {
		if !kp.remoteSubnets.Contains(inputCidrBlock) {
			kp.remoteSubnets.Add(inputCidrBlock)
		}

		gwIP := endpoint.GatewayIP()
		kp.remoteSubnetGw[inputCidrBlock] = gwIP
	}

	if err := kp.updateRoutingRulesForInterClusterSupport(endpoint.Spec.Subnets, Add); err != nil {
		klog.Errorf("updateRoutingRulesForInterClusterSupport for new remote %#v returned error: %+v",
			endpoint, err)
		return err
	}

	// Add routes to the new endpoint on the GatewayNode.
	kp.updateRoutingRulesForHostNetworkSupport(endpoint.Spec.Subnets, Add)
	kp.updateIptableRulesForInterClusterTraffic(endpoint.Spec.Subnets, Add)

	kp.remoteEndpointTimeStamp[endpoint.Spec.ClusterID] = endpoint.CreationTimestamp

	return nil
}

func (kp *SyncHandler) RemoteEndpointUpdated(endpoint *submV1.Endpoint) error {
	return nil
}

func (kp *SyncHandler) RemoteEndpointRemoved(endpoint *submV1.Endpoint) error {
	kp.syncHandlerMutex.Lock()
	defer kp.syncHandlerMutex.Unlock()

	lastProcessedTime, ok := kp.remoteEndpointTimeStamp[endpoint.Spec.ClusterID]

	if ok && lastProcessedTime.After(endpoint.CreationTimestamp.Time) {
		klog.Infof("Ignoring deleted remote %#v since a later endpoint was already"+
			"processed", endpoint)
		return nil
	}

	delete(kp.remoteEndpointTimeStamp, endpoint.Spec.ClusterID)

	for _, inputCidrBlock := range endpoint.Spec.Subnets {
		kp.remoteSubnets.Remove(inputCidrBlock)
		delete(kp.remoteSubnetGw, inputCidrBlock)
	}
	// TODO: Handle a remote endpoint removal use-case
	//         - remove related iptable rules
	if err := kp.updateRoutingRulesForInterClusterSupport(endpoint.Spec.Subnets, Delete); err != nil {
		klog.Errorf("updateRoutingRulesForInterClusterSupport for removed remote %#v returned error: %+v",
			err, endpoint)
		return err
	}

	kp.updateRoutingRulesForHostNetworkSupport(endpoint.Spec.Subnets, Delete)
	kp.updateIptableRulesForInterClusterTraffic(endpoint.Spec.Subnets, Delete)

	return nil
}

func (kp *SyncHandler) getHostIfaceIPAddress() (net.IP, error) {
	addrs, err := kp.defaultHostIface.Addrs()
	if err != nil {
		return nil, errors.Wrap(err, "error getting default host addresses")
	}

	if len(addrs) > 0 {
		for i := range addrs {
			ipAddr, _, err := net.ParseCIDR(addrs[i].String())
			if err != nil {
				klog.Errorf("Unable to ParseCIDR  %v: %v", addrs, err)
			}

			if ipAddr.To4() != nil {
				return ipAddr, nil
			}
		}
	}

	return nil, nil
}
