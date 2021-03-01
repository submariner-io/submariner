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
package kubeproxy

import (
	"fmt"
	"net"

	"k8s.io/klog"

	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/util"
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
				return fmt.Errorf("failed to delete the the vxlan interface that points to old endpoint %s : %v",
					kp.vxlanDevice.activeEndpointHostname, err)
			}

			kp.vxlanDevice = nil
		}

		kp.isGatewayNode = false
		localClusterGwNodeIP := net.ParseIP(endpoint.Spec.PrivateIP)
		remoteVtepIP, err := getVxlanVtepIPAddress(localClusterGwNodeIP.String())
		if err != nil {
			return fmt.Errorf("failed to derive the remoteVtepIP %v", err)
		}

		klog.Infof("Creating the vxlan interface %s with gateway node IP %s", VxLANIface, localClusterGwNodeIP)
		err = kp.createVxLANInterface(endpoint.Spec.Hostname, VxInterfaceWorker, localClusterGwNodeIP)
		if err != nil {
			klog.Fatalf("Unable to create VxLAN interface on non-GatewayNode (%s): %v", endpoint.Spec.Hostname, err)
		}

		kp.vxlanGwIP = &remoteVtepIP
		err = kp.reconcileRoutes(remoteVtepIP)
		if err != nil {
			return fmt.Errorf("error while reconciling routes %v", err)
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

	// If the vxLAN device exists and it points to the same endpoint, delete it.
	if kp.vxlanDevice != nil && kp.vxlanDevice.activeEndpointHostname == endpoint.Spec.Hostname {
		err := kp.vxlanDevice.deleteVxLanIface()
		kp.vxlanDevice = nil
		kp.vxlanGwIP = nil
		if err != nil {
			return fmt.Errorf("failed to delete the the vxlan interface on Endpoint removal: %v", err)
		}
	}

	return nil
}

func (kp *SyncHandler) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	if err := kp.overlappingSubnets(endpoint.Spec.Subnets); err != nil {
		// Skip processing the endpoint when CIDRs overlap and return nil to avoid re-queuing.
		klog.Errorf("overlappingSubnets for new remote %#v returned error: %v", endpoint, err)
		return nil
	}

	kp.syncHandlerMutex.Lock()
	defer kp.syncHandlerMutex.Unlock()
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

	return nil
}

func (kp *SyncHandler) RemoteEndpointUpdated(endpoint *submV1.Endpoint) error {
	return nil
}

func (kp *SyncHandler) RemoteEndpointRemoved(endpoint *submV1.Endpoint) error {
	kp.syncHandlerMutex.Lock()
	defer kp.syncHandlerMutex.Unlock()
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

func (kp *SyncHandler) overlappingSubnets(remoteSubnets []string) error {
	// If the remoteSubnets [*] overlap with local cluster Pod/Service CIDRs we
	// should not update the IPTable rules on the host, as it will disrupt the
	// functionality of the local cluster. So, lets validate that subnets do not
	// overlap before we program any IPTable rules on the host for inter-cluster
	// traffic.
	// [*] Note: In a non-GlobalNet deployment, remoteSubnets will be a list of
	// Pod/Service CIDRs, whereas in a GlobalNet deployment, it will be a list of
	// globalCIDRs allocated to the clusters.
	for _, serviceCidr := range kp.localServiceCidr {
		overlap, err := util.IsOverlappingCIDR(remoteSubnets, serviceCidr)
		if err != nil {
			// Ideally this case will never hit, as the subnets are valid CIDRs
			klog.Warningf("unable to validate overlapping Service CIDR: %s", err)
		}

		if overlap {
			return fmt.Errorf("local Service CIDR %q, overlaps with remote cluster subnets %s",
				serviceCidr, remoteSubnets)
		}
	}

	for _, podCidr := range kp.localClusterCidr {
		overlap, err := util.IsOverlappingCIDR(remoteSubnets, podCidr)
		if err != nil {
			klog.Warningf("unable to validate overlapping Pod CIDR: %s", err)
		}

		if overlap {
			return fmt.Errorf("local Pod CIDR %q, overlaps with remote cluster subnets %s",
				podCidr, remoteSubnets)
		}
	}

	return nil
}

func (kp *SyncHandler) getHostIfaceIPAddress() (net.IP, error) {
	addrs, err := kp.defaultHostIface.Addrs()
	if err != nil {
		return nil, err
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
