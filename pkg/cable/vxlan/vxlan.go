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
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/util"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"k8s.io/klog"
)

const (
	VxLANIface             = "submariner"
	VxLANOverhead          = 50
	VxLANVTepNetworkPrefix = 241
	CableDriverName        = "vxlan"
	TableID                = 100
)

type Operation int

const (
	Add Operation = iota
	Delete
	Flush
)

type vxLan struct {
	localEndpoint types.SubmarinerEndpoint
	connections   []v1.Connection
	mutex         sync.Mutex
	vxLanIface    *vxLanIface
	NATTPort      int `default:"4500"`
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

	port, err := localEndpoint.Spec.GetBackendPort(v1.UDPPortConfig, int32(v.NATTPort))
	if err != nil {
		return nil, fmt.Errorf("failed to get the UDP port configuration: %v", err)
	}

	if err = v.createVxLANInterface(localEndpoint.Spec.Hostname, int(port)); err != nil {
		return nil, fmt.Errorf("failed to setup Vxlan link: %v", err)
	}

	return &v, nil
}

func (v *vxLan) createVxLANInterface(activeEndPoint string, port int) error {
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
		vtepPort: port,
		mtu:      vxlanMtu,
	}

	v.vxLanIface, err = newVxlanIface(attrs, activeEndPoint)
	if err != nil {
		return fmt.Errorf("failed to create vxlan interface on Gateway Node: %v", err)
	}

	v.vxLanIface.vtepIP = vtepIP

	err = v.addIPRule()
	if err != nil && !os.IsExist(err) {
		return fmt.Errorf("failed to add ip rule: %v", err)
	}

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
		klog.Warningf("VxlanId of existing interface (%d) does not match with required VxlanId (%d)", existing.VxlanId, required.VxlanId)
		return false
	}

	if len(required.Group) > 0 && len(existing.Group) > 0 && !required.Group.Equal(existing.Group) {
		klog.Warningf("Vxlan Group (%v) of existing interface does not match with required Group (%v)", existing.Group, required.Group)
		return false
	}

	if len(required.SrcAddr) > 0 && len(existing.SrcAddr) > 0 && !required.SrcAddr.Equal(existing.SrcAddr) {
		klog.Warningf("Vxlan SrcAddr (%v) of existing interface does not match with required SrcAddr (%v)", existing.SrcAddr, required.SrcAddr)
		return false
	}

	if required.Port > 0 && existing.Port > 0 && required.Port != existing.Port {
		klog.Warningf("Vxlan Port (%d) of existing interface does not match with required Port (%d)", existing.Port, required.Port)
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

func (v *vxLan) addIPRule() error {
	if v.vxLanIface != nil {
		rule := netlink.NewRule()
		rule.Table = TableID
		rule.Priority = TableID

		err := netlink.RuleAdd(rule)
		if err != nil && !os.IsExist(err) {
			return fmt.Errorf("failed to add ip rule %s: %v", rule, err)
		}
	}

	return nil
}

func (v *vxLan) ConnectToEndpoint(endpointInfo *natdiscovery.NATEndpointInfo) (string, error) {
	remoteEndpoint := endpointInfo.Endpoint
	if v.localEndpoint.Spec.ClusterID == remoteEndpoint.Spec.ClusterID {
		klog.V(log.DEBUG).Infof("Will not connect to self")
		return "", nil
	}

	remoteIP := net.ParseIP(endpointInfo.UseIP)
	if remoteIP == nil {
		return "", fmt.Errorf("failed to parse remote IP %s", endpointInfo.UseIP)
	}

	allowedIPs := parseSubnets(remoteEndpoint.Spec.Subnets)

	klog.V(log.DEBUG).Infof("Connecting cluster %s endpoint %s",
		remoteEndpoint.Spec.ClusterID, remoteIP)
	v.mutex.Lock()
	defer v.mutex.Unlock()

	cable.RecordConnection(CableDriverName, &v.localEndpoint.Spec, &remoteEndpoint.Spec, string(v1.Connected), true)

	remoteVtepIP, err := v.getVxlanVtepIPAddress(endpointInfo.UseIP)

	if err != nil {
		return endpointInfo.UseIP, fmt.Errorf("failed to derive the vxlan vtepIP for %s, %v", endpointInfo.UseIP, err)
	}

	err = v.vxLanIface.AddFDB(remoteIP, "00:00:00:00:00:00")

	if err != nil {
		return endpointInfo.UseIP, fmt.Errorf("failed to add remoteIP %q to the forwarding database", remoteIP)
	}

	err = v.vxLanIface.AddRoute(allowedIPs, remoteVtepIP, v.vxLanIface.vtepIP)

	if err != nil {
		return endpointInfo.UseIP, fmt.Errorf("failed to add route for the CIDR %q with remoteVtepIP %q and vxlanInterfaceIP %q",
			allowedIPs, remoteVtepIP, v.vxLanIface.vtepIP)
	}

	v.connections = append(v.connections, v1.Connection{Endpoint: remoteEndpoint.Spec, Status: v1.Connected,
		UsingIP: endpointInfo.UseIP, UsingNAT: endpointInfo.UseNAT})

	klog.V(log.DEBUG).Infof("Done adding endpoint for cluster %s", remoteEndpoint.Spec.ClusterID)

	return endpointInfo.UseIP, nil
}

func (v *vxLan) DisconnectFromEndpoint(remoteEndpoint types.SubmarinerEndpoint) error {
	klog.V(log.DEBUG).Infof("Removing endpoint %#v", remoteEndpoint)

	if v.localEndpoint.Spec.ClusterID == remoteEndpoint.Spec.ClusterID {
		klog.V(log.DEBUG).Infof("Will not disconnect self")
		return nil
	}

	v.mutex.Lock()
	defer v.mutex.Unlock()

	// parse remote addresses and allowed IPs
	ip := submarinerEndpointIP(&remoteEndpoint)

	remoteIP := net.ParseIP(ip)

	if remoteIP == nil {
		return fmt.Errorf("failed to parse remote IP %s", ip)
	}

	allowedIPs := parseSubnets(remoteEndpoint.Spec.Subnets)

	err := v.vxLanIface.DelFDB(remoteIP, "00:00:00:00:00:00")

	if err != nil {
		return fmt.Errorf("failed to delete remoteIP %q from the forwarding database", remoteIP)
	}

	err = v.vxLanIface.DelRoute(allowedIPs)

	if err != nil {
		return fmt.Errorf("failed to remove route for the CIDR %q",
			allowedIPs)
	}

	v.connections = removeConnectionForEndpoint(v.connections, remoteEndpoint)
	cable.RecordDisconnected(CableDriverName, &v.localEndpoint.Spec, &remoteEndpoint.Spec)

	klog.V(log.DEBUG).Infof("Done removing endpoint for cluster %s", remoteEndpoint.Spec.ClusterID)

	return nil
}

func removeConnectionForEndpoint(connections []v1.Connection, endpoint types.SubmarinerEndpoint) []v1.Connection {
	for j := range connections {
		if connections[j].Endpoint.CableName == endpoint.Spec.CableName {
			copy(connections[j:], connections[j+1:])
			return connections[:len(connections)-1]
		}
	}

	return connections
}

func (v *vxLan) GetConnections() ([]v1.Connection, error) {
	return v.connections, nil
}

func (v *vxLan) GetActiveConnections() ([]v1.Connection, error) {
	return v.connections, nil
}

func (iface *vxLanIface) AddFDB(ipAddress net.IP, hwAddr string) error {
	macAddr, err := net.ParseMAC(hwAddr)

	if err != nil {
		return fmt.Errorf("invalid MAC Address (%s) supplied. %v", hwAddr, err)
	}

	if ipAddress == nil {
		return fmt.Errorf("invalid ipAddress (%v) supplied", ipAddress)
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

	err = netlink.NeighAppend(neigh)
	if err != nil {
		return fmt.Errorf("unable to add the bridge fdb entry %v, err: %s", neigh, err)
	} else {
		klog.V(log.DEBUG).Infof("Successfully added the bridge fdb entry %v", neigh)
	}

	return nil
}

func (iface *vxLanIface) DelFDB(ipAddress net.IP, hwAddr string) error {
	macAddr, err := net.ParseMAC(hwAddr)
	if err != nil {
		return fmt.Errorf("invalid MAC Address (%s) supplied. %v", hwAddr, err)
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

	err = netlink.NeighDel(neigh)
	if err != nil {
		return fmt.Errorf("unable to delete the bridge fdb entry %v, err: %s", neigh, err)
	} else {
		klog.V(log.DEBUG).Infof("Successfully deleted the bridge fdb entry %v", neigh)
	}

	return nil
}

func (iface *vxLanIface) AddRoute(ipAddressList []net.IPNet, gwIP, ips net.IP) error {
	for i := range ipAddressList {
		route := &netlink.Route{
			LinkIndex: iface.link.Index,
			Src:       ips,
			Dst:       &ipAddressList[i],
			Gw:        gwIP,
			Type:      netlink.NDA_DST,
			Flags:     netlink.NTF_SELF,
			Priority:  100,
			Table:     TableID,
		}
		err := netlink.RouteAdd(route)

		if err == syscall.EEXIST {
			err = netlink.RouteReplace(route)
		}

		if err != nil {
			return fmt.Errorf("unable to add the route entry %v, err: %s", route, err)
		} else {
			klog.V(log.DEBUG).Infof("Successfully added the route entry %v and gw ip %v", route, gwIP)
		}
	}

	return nil
}

func (iface *vxLanIface) DelRoute(ipAddressList []net.IPNet) error {
	for i := range ipAddressList {
		route := &netlink.Route{
			LinkIndex: iface.link.Index,
			Dst:       &ipAddressList[i],
			Gw:        nil,
			Type:      netlink.NDA_DST,
			Flags:     netlink.NTF_SELF,
			Priority:  100,
			Table:     TableID,
		}
		err := netlink.RouteDel(route)
		if err != nil {
			return fmt.Errorf("unable to add the route entry %v, err: %s", route, err)
		} else {
			klog.V(log.DEBUG).Infof("Successfully deleted the route entry %v", route)
		}
	}

	return nil
}

func (v *vxLan) Init() error {
	return nil
}

func (v *vxLan) GetName() string {
	return CableDriverName
}

func submarinerEndpointIP(ep *types.SubmarinerEndpoint) string {
	if ep.Spec.NATEnabled {
		return ep.Spec.PublicIP
	}

	return ep.Spec.PrivateIP
}

// parse CIDR string and skip errors
func parseSubnets(subnets []string) []net.IPNet {
	nets := make([]net.IPNet, 0, len(subnets))

	for _, sn := range subnets {
		_, cidr, err := net.ParseCIDR(sn)
		if err != nil {
			// this should not happen. Log and continue
			klog.Errorf("failed to parse subnet %s: %v", sn, err)
			continue
		}

		nets = append(nets, *cidr)
	}

	return nets
}
