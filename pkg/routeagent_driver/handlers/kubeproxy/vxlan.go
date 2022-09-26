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
	goerrors "errors"
	"net"
	"strconv"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	"github.com/submariner-io/submariner/pkg/port"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type vxLanAttributes struct {
	name     string
	vxlanID  int
	group    net.IP
	srcAddr  net.IP
	vtepPort int
	mtu      int
}

type vxLanIface struct {
	netLink                netlinkAPI.Interface
	link                   *netlink.Vxlan
	activeEndpointHostname string
}

func (kp *SyncHandler) newVxlanIface(attrs *vxLanAttributes, activeEndPoint string) (*vxLanIface, error) {
	iface := &netlink.Vxlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:  attrs.name,
			MTU:   attrs.mtu,
			Flags: net.FlagUp,
		},
		VxlanId: attrs.vxlanID,
		SrcAddr: attrs.srcAddr,
		Group:   attrs.group,
		Port:    attrs.vtepPort,
	}

	vxLANIface := &vxLanIface{
		netLink:                kp.netLink,
		link:                   iface,
		activeEndpointHostname: activeEndPoint,
	}

	if err := kp.createVxLanIface(vxLANIface); err != nil {
		return nil, err
	}

	// ip link set $vxLANIface up
	if err := kp.netLink.LinkSetUp(vxLANIface.link); err != nil {
		return nil, errors.Wrap(err, "failed to bring up VxLAN interface")
	}

	return vxLANIface, nil
}

func (kp *SyncHandler) createVxLanIface(iface *vxLanIface) error {
	err := kp.netLink.LinkAdd(iface.link)
	if goerrors.Is(err, syscall.EEXIST) {
		// Get the properties of existing vxlan interface
		existing, err := kp.netLink.LinkByName(iface.link.Name)
		if err != nil {
			return errors.Wrap(err, "failed to retrieve link info")
		}

		if isVxlanConfigTheSame(iface.link, existing) {
			logger.V(log.DEBUG).Infof("VxLAN interface already exists with same configuration.")

			iface.link = existing.(*netlink.Vxlan)

			return nil
		}

		// Config does not match, delete the existing interface and re-create it.
		if err = kp.netLink.LinkDel(existing); err != nil {
			return errors.Wrap(err, "failed to delete the existing vxlan interface")
		}

		if err = kp.netLink.LinkAdd(iface.link); err != nil {
			return errors.Wrap(err, "failed to re-create the the vxlan interface")
		}
	} else if err != nil {
		return errors.Wrap(err, "failed to create the the vxlan interface")
	}

	return nil
}

func (iface *vxLanIface) deleteVxLanIface() error {
	err := iface.netLink.LinkDel(iface.link)
	if err != nil {
		return errors.Wrap(err, "failed to delete the the vxlan interface")
	}

	return nil
}

func isVxlanConfigTheSame(newLink, currentLink netlink.Link) bool {
	required := newLink.(*netlink.Vxlan)
	existing := currentLink.(*netlink.Vxlan)

	if required.VxlanId != existing.VxlanId {
		logger.Errorf(nil, "VxlanId of existing interface (%d) does not match with required VxlanId (%d)",
			existing.VxlanId, required.VxlanId)
		return false
	}

	if len(required.Group) > 0 && len(existing.Group) > 0 && !required.Group.Equal(existing.Group) {
		logger.Errorf(nil, "Vxlan Group (%v) of existing interface does not match with required Group (%v)",
			existing.Group, required.Group)
		return false
	}

	if len(required.SrcAddr) > 0 && len(existing.SrcAddr) > 0 && !required.SrcAddr.Equal(existing.SrcAddr) {
		logger.Errorf(nil, "Vxlan SrcAddr (%v) of existing interface does not match with required SrcAddr (%v)",
			existing.SrcAddr, required.SrcAddr)
		return false
	}

	if required.Port > 0 && existing.Port > 0 && required.Port != existing.Port {
		logger.V(log.DEBUG).Infof("Vxlan Port (%d) of existing interface does not match with required Port (%d)",
			existing.Port, required.Port)
		return false
	}

	return true
}

func (iface *vxLanIface) configureIPAddress(ipAddress net.IP, mask net.IPMask) error {
	ipConfig := &netlink.Addr{IPNet: &net.IPNet{
		IP:   ipAddress,
		Mask: mask,
	}}

	err := iface.netLink.AddrAdd(iface.link, ipConfig)
	if goerrors.Is(err, syscall.EEXIST) {
		return nil
	} else if err != nil {
		return errors.Wrapf(err, "unable to configure address (%s) on vxlan interface (%s)", ipAddress, iface.link.Name)
	}

	return nil
}

func (iface *vxLanIface) AddFDB(ipAddress net.IP, hwAddr string) error {
	macAddr, err := net.ParseMAC(hwAddr)
	if err != nil {
		return errors.Wrapf(err, "invalid MAC Address (%s) supplied", hwAddr)
	}

	if ipAddress == nil {
		return errors.Errorf("invalid ipAddress (%v) supplied", ipAddress)
	}

	neigh := &netlink.Neigh{
		LinkIndex:    iface.link.Index,
		Family:       unix.AF_BRIDGE,
		Flags:        netlink.NTF_SELF,
		Type:         netlink.NDA_DST,
		IP:           ipAddress,
		State:        netlink.NUD_PERMANENT | netlink.NUD_NOARP,
		HardwareAddr: macAddr,
	}

	err = iface.netLink.NeighAppend(neigh)
	if err != nil {
		return errors.Wrapf(err, "unable to add the bridge fdb entry %v", neigh)
	}

	logger.V(log.DEBUG).Infof("Successfully added the bridge fdb entry %v", neigh)

	return nil
}

func (iface *vxLanIface) DelFDB(ipAddress net.IP, hwAddr string) error {
	macAddr, err := net.ParseMAC(hwAddr)
	if err != nil {
		return errors.Wrapf(err, "invalid MAC Address (%s) supplied", hwAddr)
	}

	neigh := &netlink.Neigh{
		LinkIndex:    iface.link.Index,
		Family:       unix.AF_BRIDGE,
		Flags:        netlink.NTF_SELF,
		Type:         netlink.NDA_DST,
		IP:           ipAddress,
		State:        netlink.NUD_PERMANENT | netlink.NUD_NOARP,
		HardwareAddr: macAddr,
	}

	err = iface.netLink.NeighDel(neigh)
	if err != nil {
		return errors.Wrapf(err, "unable to delete the bridge fdb entry %v", neigh)
	}

	logger.V(log.DEBUG).Infof("Successfully deleted the bridge fdb entry %v", neigh)

	return nil
}

func getVxlanVtepIPAddress(ipAddr string) (net.IP, error) {
	ipSlice := strings.Split(ipAddr, ".")
	if len(ipSlice) < 4 {
		return nil, errors.Errorf("invalid ipAddr [%s]", ipAddr)
	}

	ipSlice[0] = strconv.Itoa(VxLANVTepNetworkPrefix)
	vxlanIP := net.ParseIP(strings.Join(ipSlice, "."))

	return vxlanIP, nil
}

func (kp *SyncHandler) createVxLANInterface(activeEndPoint string, ifaceType int, gatewayNodeIP net.IP) error {
	ipAddr, err := kp.getHostIfaceIPAddress()
	if err != nil {
		return errors.Wrap(err, "unable to retrieve the IPv4 address on the Host")
	}

	vtepIP, err := getVxlanVtepIPAddress(ipAddr.String())
	if err != nil {
		return errors.Wrapf(err, "failed to derive the vxlan vtepIP for %s", ipAddr)
	}

	// Derive the MTU based on the default outgoing interface
	vxlanMtu := kp.defaultHostIface.MTU - VxLANOverhead

	if ifaceType == VxInterfaceGateway {
		attrs := &vxLanAttributes{
			name:     VxLANIface,
			vxlanID:  100,
			group:    nil,
			srcAddr:  nil,
			vtepPort: port.IntraClusterVxLAN,
			mtu:      vxlanMtu,
		}

		kp.vxlanDevice, err = kp.newVxlanIface(attrs, activeEndPoint)
		if err != nil {
			return errors.Wrap(err, "failed to create vxlan interface on Gateway Node")
		}

		for _, fdbAddress := range kp.remoteVTEPs.Elements() {
			err = kp.vxlanDevice.AddFDB(net.ParseIP(fdbAddress), "00:00:00:00:00:00")
			if err != nil {
				return errors.Wrap(err, "failed to add FDB entry on the Gateway Node vxlan iface")
			}
		}

		err = kp.netLink.EnableLooseModeReversePathFilter(VxLANIface)
		if err != nil {
			return errors.Wrap(err, "error enabling loose mode")
		}

		logger.V(log.DEBUG).Infof("Successfully configured reverse path filter to loose mode on %q", VxLANIface)
	} else if ifaceType == VxInterfaceWorker {
		// non-Gateway/Worker Node
		attrs := &vxLanAttributes{
			name:     VxLANIface,
			vxlanID:  100,
			group:    gatewayNodeIP,
			srcAddr:  nil,
			vtepPort: port.IntraClusterVxLAN,
			mtu:      vxlanMtu,
		}

		kp.vxlanDevice, err = kp.newVxlanIface(attrs, activeEndPoint)
		if err != nil {
			return errors.Wrap(err, "failed to create vxlan interface on non-Gateway Node")
		}
	}

	err = kp.vxlanDevice.configureIPAddress(vtepIP, net.CIDRMask(8, 32))
	if err != nil {
		return errors.Wrap(err, "failed to configure vxlan interface ipaddress on the Gateway Node")
	}

	return nil
}
