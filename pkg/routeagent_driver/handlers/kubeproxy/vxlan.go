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
	"github.com/submariner-io/submariner/pkg/port"
	"github.com/submariner-io/submariner/pkg/vxlan"
	k8snet "k8s.io/utils/net"
)

func (kp *SyncHandler) newVxlanInterface(attrs *vxlan.Attributes) (*vxlan.Interface, error) {
	vxlanInterface, err := vxlan.NewInterface(attrs, kp.netLink)
	if err != nil {
		return nil, errors.Wrap(err, "error creating VxLAN interface")
	}

	// ip link set $vxLANIface up
	if err = vxlanInterface.SetupLink(); err != nil {
		return nil, errors.Wrap(err, "failed to bring up VxLAN interface")
	}

	return vxlanInterface, nil
}

func (kp *SyncHandler) createVxLANInterface(ifaceType int, gatewayNodeIP net.IP) error {
	ipAddr, err := kp.getHostIfaceIPAddress()
	if err != nil {
		return errors.Wrapf(err, "unable to retrieve the IPv%s address on the Host", kp.ipFamily)
	}

	vtepIP, err := vxlan.GetVtepIPAddressFrom(ipAddr.String(), kp.vtepPrefixCIDR, kp.ipFamily)
	if err != nil {
		return errors.Wrapf(err, "failed to derive the vxlan vtepIP for %s", ipAddr)
	}

	if ifaceType == VxInterfaceGateway {
		attrs := &vxlan.Attributes{
			Name:     kp.vxlanIface,
			VxlanID:  100,
			VtepPort: port.IntraClusterVxLAN,
			Mtu:      kp.defaultHostIface.MTU(),
		}

		// For IPv6 VxLAN interface SrcAddr should be set with local IP
		if kp.ipFamily == k8snet.IPv6 {
			attrs.SrcAddr = *kp.vxlanGwIP
		}

		kp.vxlanDevice, err = kp.newVxlanInterface(attrs)
		if err != nil {
			return errors.Wrap(err, "failed to create vxlan interface on Gateway Node")
		}

		for _, fdbAddress := range kp.remoteVTEPs.UnsortedList() {
			err = kp.vxlanDevice.AddFDB(net.ParseIP(fdbAddress), "00:00:00:00:00:00")
			if err != nil {
				return errors.Wrap(err, "failed to add FDB entry on the Gateway Node vxlan iface")
			}
		}

		err = kp.netLink.EnsureLooseModeIsConfigured(kp.vxlanIface, kp.ipFamily)
		if err != nil {
			return errors.Wrap(err, "error while validating loose mode")
		}

		logger.Infof("Successfully configured reverse path filter to loose mode on %q", kp.vxlanIface)
	} else if ifaceType == VxInterfaceWorker {
		// non-Gateway/Worker Node
		attrs := &vxlan.Attributes{
			Name:     kp.vxlanIface,
			VxlanID:  100,
			Group:    gatewayNodeIP,
			VtepPort: port.IntraClusterVxLAN,
			Mtu:      kp.defaultHostIface.MTU(),
		}

		kp.vxlanDevice, err = kp.newVxlanInterface(attrs)
		if err != nil {
			return errors.Wrap(err, "failed to create vxlan interface on non-Gateway Node")
		}
	}

	_, ipNet, err := net.ParseCIDR(kp.vtepPrefixCIDR)
	if err != nil {
		return errors.Wrapf(err, "invalid VTEP CIDR %q", kp.vtepPrefixCIDR)
	}

	err = kp.vxlanDevice.ConfigureIPAddress(vtepIP, ipNet.Mask)
	if err != nil {
		return errors.Wrap(err, "failed to configure vxlan interface ipaddress on the Gateway Node")
	}

	err = kp.netLink.EnableForwarding(kp.vxlanIface, kp.ipFamily)
	if err != nil {
		return errors.Wrapf(err, "error enabling forwarding on the %q iface", kp.vxlanIface)
	}

	return nil
}
