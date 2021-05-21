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

package ovn

import (
	"net"
	"syscall"

	"github.com/pkg/errors"
	npSyncerOVN "github.com/submariner-io/submariner/pkg/networkplugin-syncer/handlers/ovn"
	"github.com/vishvananda/netlink"

	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/ovn/vsctl"
)

func (ovn *Handler) getMTU() (int, error) {
	if link, err := ovn.netlink.LinkByName(OVNK8sMgmntIntfName); err != nil {
		return 0, errors.Wrapf(err, "error retrieving link by name %q", OVNK8sMgmntIntfName)
	} else {
		return link.Attrs().MTU, nil
	}
}

func (ovn *Handler) ensureSubmarinerNodeBridge() error {
	mtu, err := ovn.getMTU()
	if err != nil {
		return errors.Wrap(err, "error finding OVN MTU")
	}

	err = vsctl.AddBridge(ovnK8sSubmarinerBridge)
	if err != nil {
		return errors.Wrapf(err, "error creating Submariner %q bridge", ovnK8sSubmarinerBridge)
	}

	err = vsctl.AddInternalPort(ovnK8sSubmarinerBridge, ovnK8sSubmarinerInterface, ovnK8sSubmarinerInterfaceMAC, mtu)
	if err != nil {
		return errors.Wrapf(err, "error creating Submariner port %q on the node", ovnK8sSubmarinerInterface)
	}

	err = vsctl.AddOVNBridgeMapping(npSyncerOVN.SubmarinerUpstreamLocalnet, ovnK8sSubmarinerBridge)
	if err != nil {
		return errors.Wrap(err, "error mapping Submariner bridge to OVN localnet")
	}

	return ovn.configureSubmarinerInterface()
}

func (ovn *Handler) configureSubmarinerInterface() error {
	ovnLink, err := ovn.netlink.LinkByName(ovnK8sSubmarinerInterface)
	if err != nil {
		return errors.Wrapf(err, "error looking up Submariner interface %q", ovnK8sSubmarinerInterface)
	}

	if err := ovn.netlink.LinkSetUp(ovnLink); err != nil {
		return errors.Wrapf(err, "error bringing up interface %q", ovnK8sSubmarinerInterface)
	}

	ipAddress, ipNet, err := net.ParseCIDR(npSyncerOVN.HostUpstreamNET)
	if err != nil {
		return errors.Wrapf(err, "error parsing submariner interface address %q", npSyncerOVN.HostUpstreamNET)
	}

	ipConfig := &netlink.Addr{IPNet: &net.IPNet{
		IP:   ipAddress,
		Mask: ipNet.Mask,
	}}

	if err := ovn.netlink.AddrAdd(ovnLink, ipConfig); err != nil && err != syscall.EEXIST {
		return errors.Wrapf(err, "unable to configure address %q on Submariner interface %q", ipAddress, ovnK8sSubmarinerInterface)
	}

	return nil
}
