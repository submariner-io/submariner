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

// On any endpoint event the routes and FDB entries on vx-submarier should be
// reconciled
func (kp *SyncHandler) LocalEndpointCreated(endpoint *submV1.Endpoint) error {
	kp.syncHandlerMutex.Lock()
	defer kp.syncHandlerMutex.Unlock()

	localClusterGwNodeIP := net.ParseIP(endpoint.Spec.PrivateIP)
	kp.addGwIp(localClusterGwNodeIP.String())

	// Try and let all handlers know we're a gateway as fast as possible
	// Either an endpoint with our hostname is added or transitionToGw is called
	if endpoint.Spec.Hostname == kp.hostname {
		kp.isGatewayNode = true
	}

	kp.localCableDriver = endpoint.Spec.Backend

	klog.Infof("Updating the vxlan interface %s and fdb entries", VxLANIface)

	// creates or updates the physical interface and fdb entries
	err := kp.updateVxLANInterface()
	if err != nil {
		klog.Fatalf("Unable to update VxLAN interface on non-GatewayNode (%s): %v", endpoint.Spec.Hostname, err)
	}

	// In the routeagent we only need to reconcileRoutes on non-gateway nodes
	err = kp.reconcileIntraClusterRoutes()
	if err != nil {
		return errors.Wrap(err, "error while reconciling routes")
	}

	return nil
}

func (kp *SyncHandler) LocalEndpointUpdated(endpoint *submV1.Endpoint) error {
	return nil
}

func (kp *SyncHandler) LocalEndpointRemoved(endpoint *submV1.Endpoint) error {
	kp.syncHandlerMutex.Lock()
	defer kp.syncHandlerMutex.Unlock()
	localClusterGwNodeIP := net.ParseIP(endpoint.Spec.PrivateIP)
	kp.removeGwIp(localClusterGwNodeIP.String())

	if endpoint.Spec.Hostname == kp.hostname {
		kp.isGatewayNode = false
	}

	err := kp.updateVxLANInterface()
	if err != nil {
		klog.Fatalf("Unable to update VxLAN interface on non-GatewayNode (%s): %v", endpoint.Spec.Hostname, err)
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

	//lastProcessedTime, ok := kp.remoteEndpointTimeStamp[endpoint.Spec.ClusterID]

	// if ok && lastProcessedTime.After(endpoint.CreationTimestamp.Time) {
	// 	klog.Infof("Ignoring new remote %#v since a later endpoint was already"+
	// 		"processed", endpoint)
	// 	return nil
	// }

	for _, inputCidrBlock := range endpoint.Spec.Subnets {
		if !kp.remoteSubnets.Contains(inputCidrBlock) {
			kp.remoteSubnets.Add(inputCidrBlock)
		}

		gwIP := endpoint.GatewayIP()
		kp.remoteSubnetGw[inputCidrBlock] = gwIP
	}

	// we will reconcile correctly on local endpoint creation if the vx-submariner
	// device has not been created
	if kp.vxlanDevice != nil {
		err := kp.reconcileIntraClusterRoutes()
		if err != nil {
			return errors.Wrap(err, "error while reconciling routes")
		}
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
	// TODO (astoycos): Handle a remote endpoint removal use-case
	//         - remove related iptable rules
	if kp.vxlanDevice != nil {
		err := kp.reconcileIntraClusterRoutes()
		if err != nil {
			return errors.Wrap(err, "error while reconciling routes")
		}
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
