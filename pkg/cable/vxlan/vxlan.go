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
package vxlan

import (
	"fmt"
	"io/ioutil"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/util"
	"github.com/vishvananda/netlink"
	"k8s.io/klog"
)

const (
	VxLANIface             = "vxlan-tunnel"
	VxLANPort              = 4800
	VxLANOverhead          = 50
	VxLANVTepNetworkPrefix = 241
	CableDriverName        = "vxlan"
)

type vxLan struct {
	localEndpoint types.SubmarinerEndpoint
	connections   []v1.Connection
	mutex         sync.Mutex
	vxLanIface    *vxLanIface
}

type vxLanIface struct {
	activeEndpointHostname string
	link                   *netlink.Vxlan
	vtepIP                 net.IP
}

type vxLanAttributes struct {
	name     string
	vxlanID  int
	group    net.IP
	srcAddr  net.IP
	vtepPort int
	mtu      int
}

func init() {
	cable.AddDriver(CableDriverName, NewDriver)
}

func NewDriver(localEndpoint types.SubmarinerEndpoint, localCluster types.SubmarinerCluster) (cable.Driver, error) {
	var err error

	v := vxLan{
		localEndpoint: localEndpoint,
	}

	if err = v.createVxLANInterface(localEndpoint.Spec.Hostname); err != nil {
		return nil, fmt.Errorf("failed to setup Vxlan link: %v", err)
	}

	return &v, nil
}

func (v *vxLan) createVxLANInterface(activeEndPoint string) error {
	ipAddr := v.localEndpoint.Spec.PrivateIP

	vtepIP, err := v.getVxlanVtepIPAddress(ipAddr)
	if err != nil {
		return fmt.Errorf("failed to derive the vxlan vtepIP for %s, %v", ipAddr, err)
	}

	defaultHostIface, err := util.GetDefaultGatewayInterface()

	if err != nil {
		return errors.Wrapf(err, "Unable to find the default interface on host: %s",
			v.localEndpoint.Spec.Hostname)
	}

	// Derive the MTU based on the default outgoing interface
	vxlanMtu := defaultHostIface.MTU - VxLANOverhead

	attrs := &vxLanAttributes{
		name:     VxLANIface,
		vxlanID:  1000,
		group:    nil,
		srcAddr:  nil,
		vtepPort: VxLANPort,
		mtu:      vxlanMtu,
	}

	v.vxLanIface, err = newVxlanIface(attrs, activeEndPoint)
	if err != nil {
		return fmt.Errorf("failed to create vxlan interface on Gateway Node: %v", err)
	}

	v.vxLanIface.vtepIP = vtepIP
	// Enable loose mode (rp_filter=2) reverse path filtering on the vxlan interface.
	// We won't ever create rp_filter, and its permissions are 644
	// #nosec G306

	err = ioutil.WriteFile("/proc/sys/net/ipv4/conf/"+VxLANIface+"/rp_filter", []byte("2"), 0600)
	if err != nil {
		return fmt.Errorf("unable to update vxlan rp_filter proc entry, err: %s", err)
	} else {
		klog.V(log.DEBUG).Infof("Successfully configured rp_filter to loose mode(2) on %s", VxLANIface)
	}

	err = v.vxLanIface.configureIPAddress(vtepIP, net.CIDRMask(8, 32))
	if err != nil {
		return fmt.Errorf("failed to configure vxlan interface ipaddress on the Gateway Node %v", err)
	}

	return nil
}

func newVxlanIface(attrs *vxLanAttributes, activeEndPoint string) (*vxLanIface, error) {
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
		link:                   iface,
		activeEndpointHostname: activeEndPoint,
	}

	if err := createVxLanIface(vxLANIface); err != nil {
		return nil, err
	}

	return vxLANIface, nil
}

func createVxLanIface(iface *vxLanIface) error {
	err := netlink.LinkAdd(iface.link)
	if err == syscall.EEXIST {
		klog.Errorf("Got error: %v, %v", err, iface.link)
		// Get the properties of existing vxlan interface
		existing, err := netlink.LinkByName(iface.link.Name)
		if err != nil {
			return fmt.Errorf("failed to retrieve link info: %v", err)
		}

		if isVxlanConfigTheSame(iface.link, existing) {
			klog.V(log.DEBUG).Infof("VxLAN interface already exists with same configuration.")

			iface.link = existing.(*netlink.Vxlan)

			return nil
		}

		// Config does not match, delete the existing interface and re-create it.
		if err = netlink.LinkDel(existing); err != nil {
			return fmt.Errorf("failed to delete the existing vxlan interface: %v", err)
		}

		if err = netlink.LinkAdd(iface.link); err != nil {
			return fmt.Errorf("failed to re-create the the vxlan interface: %v", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to create the the vxlan interface: %v", err)
	}

	return nil
}

func isVxlanConfigTheSame(newLink, currentLink netlink.Link) bool {
	required := newLink.(*netlink.Vxlan)
	existing := currentLink.(*netlink.Vxlan)

	if required.VxlanId != existing.VxlanId {
		klog.Errorf("VxlanId of existing interface (%d) does not match with required VxlanId (%d)", existing.VxlanId, required.VxlanId)
		return false
	}

	if len(required.Group) > 0 && len(existing.Group) > 0 && !required.Group.Equal(existing.Group) {
		klog.Errorf("Vxlan Group (%v) of existing interface does not match with required Group (%v)", existing.Group, required.Group)
		return false
	}

	if len(required.SrcAddr) > 0 && len(existing.SrcAddr) > 0 && !required.SrcAddr.Equal(existing.SrcAddr) {
		klog.Errorf("Vxlan SrcAddr (%v) of existing interface does not match with required SrcAddr (%v)", existing.SrcAddr, required.SrcAddr)
		return false
	}

	if required.Port > 0 && existing.Port > 0 && required.Port != existing.Port {
		klog.V(log.DEBUG).Infof("Vxlan Port (%d) of existing interface does not match with required Port (%d)", existing.Port, required.Port)
		return false
	}

	return true
}

func (iface *vxLanIface) configureIPAddress(ipAddress net.IP, mask net.IPMask) error {
	ipConfig := &netlink.Addr{IPNet: &net.IPNet{
		IP:   ipAddress,
		Mask: mask,
	}}

	err := netlink.AddrAdd(iface.link, ipConfig)
	if err == syscall.EEXIST {
		return nil
	} else if err != nil {
		return fmt.Errorf("unable to configure address (%s) on vxlan interface (%s). %v", ipAddress, iface.link.Name, err)
	}

	return nil
}

func (v *vxLan) getVxlanVtepIPAddress(ipAddr string) (net.IP, error) {
	ipSlice := strings.Split(ipAddr, ".")
	if len(ipSlice) < 4 {
		return nil, fmt.Errorf("invalid ipAddr [%s]", ipAddr)
	}

	ipSlice[0] = strconv.Itoa(VxLANVTepNetworkPrefix)
	vxlanIP := net.ParseIP(strings.Join(ipSlice, "."))

	return vxlanIP, nil
}
