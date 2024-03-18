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

package vxlan

import (
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type Attributes struct {
	Name     string
	VxlanID  int
	Group    net.IP
	SrcAddr  net.IP
	VtepPort int
	Mtu      int
}

type Interface struct {
	netLink netlinkAPI.Interface
	link    *netlink.Vxlan
}

const MTUOverhead = 50

var logger = log.Logger{Logger: logf.Log.WithName("VxlanAPI")}

func NewInterface(attrs *Attributes, netLink netlinkAPI.Interface) (*Interface, error) {
	link, err := createLinkDevice(attrs, netLink)
	if err != nil {
		return nil, err
	}

	return &Interface{
		netLink: netLink,
		link:    link,
	}, nil
}

func createLinkDevice(attrs *Attributes, netLink netlinkAPI.Interface) (*netlink.Vxlan, error) {
	link := &netlink.Vxlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:  attrs.Name,
			MTU:   attrs.Mtu - MTUOverhead,
			Flags: net.FlagUp,
		},
		VxlanId: attrs.VxlanID,
		SrcAddr: attrs.SrcAddr,
		Group:   attrs.Group,
		Port:    attrs.VtepPort,
	}

	err := netLink.LinkAdd(link)

	if errors.Is(err, syscall.EEXIST) {
		// Get the properties of existing vxlan interface.
		existing, err := netLink.LinkByName(link.Name)
		if err != nil {
			return nil, errors.Wrapf(err, "error retrieving vxlan link by name %q", link.Name)
		}

		if isVxlanConfigTheSame(link, existing) {
			logger.V(log.DEBUG).Infof("VxLAN interface already exists with same configuration")
			return existing.(*netlink.Vxlan), nil
		}

		// Config does not match, delete the existing interface and re-create it.
		if err = netLink.LinkDel(existing); err != nil {
			return nil, errors.Wrapf(err, "error deleting existing vxlan device %q", existing.Attrs().Name)
		}

		err = netLink.LinkAdd(link)

		return link, errors.Wrapf(err, "error re-creating vxlan device %q", link.Name)
	}

	return link, errors.Wrapf(err, "error creating vxlan device %q", link.Name)
}

func isVxlanConfigTheSame(newLink, currentLink netlink.Link) bool {
	required := newLink.(*netlink.Vxlan)
	existing := currentLink.(*netlink.Vxlan)

	if required.VxlanId != existing.VxlanId {
		logger.Warningf("VxlanId of existing interface (%d) does not match with required VxlanId (%d)",
			existing.VxlanId, required.VxlanId)
		return false
	}

	if !required.Group.Equal(existing.Group) {
		logger.Warningf("Vxlan Group (%v) of existing interface does not match with required Group (%v)",
			existing.Group, required.Group)
		return false
	}

	if !required.SrcAddr.Equal(existing.SrcAddr) {
		logger.Warningf("Vxlan SrcAddr (%v) of existing interface does not match with required SrcAddr (%v)",
			existing.SrcAddr, required.SrcAddr)
		return false
	}

	if required.Port != existing.Port {
		logger.Warningf("Vxlan Port (%d) of existing interface does not match with required Port (%d)",
			existing.Port, required.Port)
		return false
	}

	return true
}

func GetVtepIPAddressFrom(ipAddr string, networkPrefix int) (net.IP, error) {
	ipSlice := strings.Split(ipAddr, ".")
	if len(ipSlice) < 4 {
		return nil, errors.Errorf("invalid ipAddr %q", ipAddr)
	}

	ipSlice[0] = strconv.Itoa(networkPrefix)

	return net.ParseIP(strings.Join(ipSlice, ".")), nil
}

func (i *Interface) ConfigureIPAddress(ipAddress net.IP, mask net.IPMask) error {
	ipConfig := &netlink.Addr{IPNet: &net.IPNet{
		IP:   ipAddress,
		Mask: mask,
	}}

	err := i.netLink.AddrAdd(i.link, ipConfig)
	if errors.Is(err, syscall.EEXIST) {
		return nil
	}

	return errors.Wrapf(err, "unable to configure address %q on vxlan device %q", ipAddress, i.link.Name)
}

func (i *Interface) SetupLink() error {
	return i.netLink.LinkSetUp(i.link) //nolint:wrapcheck // No need to wrap here.
}

func (i *Interface) DeleteLinkDevice() error {
	err := i.netLink.LinkDel(i.link)
	return errors.Wrapf(err, "error deleting vxlan device %q", i.link.Name)
}

func (i *Interface) AddFDB(ipAddress net.IP, hwAddr string) error {
	macAddr, err := net.ParseMAC(hwAddr)
	if err != nil {
		return errors.Wrapf(err, "invalid MAC Address %q", hwAddr)
	}

	neigh := &netlink.Neigh{
		LinkIndex:    i.link.Index,
		Family:       unix.AF_BRIDGE,
		Flags:        netlink.NTF_SELF,
		Type:         netlink.NDA_DST,
		IP:           ipAddress,
		State:        netlink.NUD_PERMANENT | netlink.NUD_NOARP,
		HardwareAddr: macAddr,
	}

	err = i.netLink.NeighAppend(neigh)
	if err != nil {
		return errors.Wrapf(err, "unable to add the bridge fdb entry %v", neigh)
	}

	logger.V(log.DEBUG).Infof("Successfully added the bridge fdb entry %v", neigh)

	return nil
}

func (i *Interface) DelFDB(ipAddress net.IP, hwAddr string) error {
	macAddr, err := net.ParseMAC(hwAddr)
	if err != nil {
		return errors.Wrapf(err, "invalid MAC Address %q", hwAddr)
	}

	neigh := &netlink.Neigh{
		LinkIndex:    i.link.Index,
		Family:       unix.AF_BRIDGE,
		Flags:        netlink.NTF_SELF,
		Type:         netlink.NDA_DST,
		IP:           ipAddress,
		State:        netlink.NUD_PERMANENT | netlink.NUD_NOARP,
		HardwareAddr: macAddr,
	}

	err = i.netLink.NeighDel(neigh)
	if err != nil {
		return errors.Wrapf(err, "unable to delete the bridge fdb entry %v", neigh)
	}

	logger.V(log.DEBUG).Infof("Successfully deleted the bridge fdb entry %v", neigh)

	return nil
}

func (i *Interface) AddRoutes(gwIP, srcIP net.IP, tableID int, destCIDRs ...net.IPNet) error {
	for j := range destCIDRs {
		route := &netlink.Route{
			LinkIndex: i.link.Index,
			Src:       srcIP,
			Dst:       &destCIDRs[j],
			Gw:        gwIP,
			Type:      netlink.NDA_DST,
			Flags:     netlink.NTF_SELF,
			Priority:  100,
			Table:     tableID,
		}

		err := i.netLink.RouteAddOrReplace(route)
		if err != nil {
			return errors.Wrapf(err, "unable to add the route entry %v", route)
		}

		logger.V(log.DEBUG).Infof("Successfully added the route entry %v and gw ip %v", route, gwIP)
	}

	return nil
}

func (i *Interface) DelRoutes(tableID int, destCIDRs ...net.IPNet) error {
	for j := range destCIDRs {
		route := &netlink.Route{
			LinkIndex: i.link.Index,
			Dst:       &destCIDRs[j],
			Gw:        nil,
			Type:      netlink.NDA_DST,
			Flags:     netlink.NTF_SELF,
			Priority:  100,
			Table:     tableID,
		}

		err := i.netLink.RouteDel(route)
		if err != nil && !os.IsNotExist(err) {
			return errors.Wrapf(err, "unable to delete the route entry %v", route)
		}

		logger.V(log.DEBUG).Infof("Successfully deleted the route entry %v", route)
	}

	return nil
}
