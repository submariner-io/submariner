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
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/cni"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"k8s.io/klog"
)

const (
	VxlanIface             = "vxlan-tunnel"
	VxlanOverhead          = 50
	VxlanVTepNetworkPrefix = 241
	CableDriverName        = "vxlan"
	TableID                = 100
	defaultPort            = 4500
)

type Operation int

const (
	Add Operation = iota
	Delete
	Flush
)

type vxlan struct {
	localEndpoint types.SubmarinerEndpoint
	localCluster  types.SubmarinerCluster
	connections   []v1.Connection
	mutex         sync.Mutex
	vxlanIface    *vxlanIface
	netLink       netlinkAPI.Interface
}

type vxlanIface struct {
	activeEndpointHostname string
	link                   *netlink.Vxlan
	vtepIP                 net.IP
}

type vxlanAttributes struct {
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

func NewDriver(localEndpoint *types.SubmarinerEndpoint, localCluster *types.SubmarinerCluster) (cable.Driver, error) {
	// We'll panic if localEndpoint or localCluster are nil, this is intentional
	var err error

	v := vxlan{
		localEndpoint: *localEndpoint,
		netLink:       netlinkAPI.New(),
		localCluster:  *localCluster,
	}

	port, err := localEndpoint.Spec.GetBackendPort(v1.UDPPortConfig, defaultPort)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get the UDP port configuration")
	}

	if err = v.createVxlanInterface(localEndpoint.Spec.Hostname, int(port)); err != nil {
		return nil, errors.Wrap(err, "failed to setup Vxlan link")
	}

	return &v, nil
}

func (v *vxlan) createVxlanInterface(activeEndPoint string, port int) error {
	ipAddr := v.localEndpoint.Spec.PrivateIP

	vtepIP, err := v.getVxlanVtepIPAddress(ipAddr)
	if err != nil {
		return errors.Wrapf(err, "failed to derive the vxlan vtepIP for %s", ipAddr)
	}

	defaultHostIface, err := netlinkAPI.GetDefaultGatewayInterface()
	if err != nil {
		return errors.Wrapf(err, "Unable to find the default interface on host: %s",
			v.localEndpoint.Spec.Hostname)
	}

	// Derive the MTU based on the default outgoing interface.
	vxlanMtu := defaultHostIface.MTU - VxlanOverhead

	attrs := &vxlanAttributes{
		name:     VxlanIface,
		vxlanID:  1000,
		group:    nil,
		srcAddr:  nil,
		vtepPort: port,
		mtu:      vxlanMtu,
	}

	v.vxlanIface, err = newVxlanIface(attrs, activeEndPoint)
	if err != nil {
		return errors.Wrap(err, "failed to create vxlan interface on Gateway Node")
	}

	v.vxlanIface.vtepIP = vtepIP

	err = v.addIPRule()
	if err != nil && !os.IsExist(err) {
		return errors.Wrap(err, "failed to add ip rule")
	}

	err = v.netLink.EnableLooseModeReversePathFilter(VxlanIface)
	if err != nil {
		return errors.Wrap(err, "unable to update vxlan rp_filter proc entry")
	}

	klog.V(log.DEBUG).Infof("Successfully configured rp_filter to loose mode(2) on %s", VxlanIface)

	err = v.vxlanIface.configureIPAddress(vtepIP, net.CIDRMask(8, 32))
	if err != nil {
		return errors.Wrap(err, "failed to configure vxlan interface ipaddress on the Gateway Node")
	}

	return nil
}

func newVxlanIface(attrs *vxlanAttributes, activeEndPoint string) (*vxlanIface, error) {
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

	vxlanIface := &vxlanIface{
		link:                   iface,
		activeEndpointHostname: activeEndPoint,
	}

	if err := createvxlanIface(vxlanIface); err != nil {
		return nil, err
	}

	return vxlanIface, nil
}

func createvxlanIface(iface *vxlanIface) error {
	err := netlink.LinkAdd(iface.link)
	if errors.Is(err, syscall.EEXIST) {
		klog.Errorf("Got error: %v, %v", err, iface.link)
		// Get the properties of existing vxlan interface.
		existing, err := netlink.LinkByName(iface.link.Name)
		if err != nil {
			return errors.Wrap(err, "failed to retrieve link info")
		}

		if isVxlanConfigTheSame(iface.link, existing) {
			klog.V(log.DEBUG).Infof("VxLAN interface already exists with same configuration.")

			iface.link = existing.(*netlink.Vxlan)

			return nil
		}

		// Config does not match, delete the existing interface and re-create it.
		if err = netlink.LinkDel(existing); err != nil {
			return errors.Wrap(err, "failed to delete the existing vxlan interface")
		}

		if err = netlink.LinkAdd(iface.link); err != nil {
			return errors.Wrap(err, "failed to re-create the the vxlan interface")
		}
	} else if err != nil {
		return errors.Wrap(err, "failed to create the the vxlan interface")
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

func (v *vxlan) getVxlanVtepIPAddress(ipAddr string) (net.IP, error) {
	ipSlice := strings.Split(ipAddr, ".")
	if len(ipSlice) < 4 {
		return nil, fmt.Errorf("invalid ipAddr [%s]", ipAddr)
	}

	ipSlice[0] = strconv.Itoa(VxlanVTepNetworkPrefix)
	vxlanIP := net.ParseIP(strings.Join(ipSlice, "."))

	return vxlanIP, nil
}

func (v *vxlan) addIPRule() error {
	if v.vxlanIface != nil {
		rule := netlink.NewRule()
		rule.Table = TableID
		rule.Priority = TableID

		err := netlink.RuleAdd(rule)
		if err != nil && !os.IsExist(err) {
			return errors.Wrapf(err, "failed to add ip rule %s", rule)
		}
	}

	return nil
}

func (v *vxlan) ConnectToEndpoint(endpointInfo *natdiscovery.NATEndpointInfo) (string, error) {
	// We'll panic if endpointInfo is nil, this is intentional
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

	privateIP := endpointInfo.Endpoint.Spec.PrivateIP

	remoteVtepIP, err := v.getVxlanVtepIPAddress(privateIP)
	if err != nil {
		return endpointInfo.UseIP, fmt.Errorf("failed to derive the vxlan vtepIP for %s: %w", privateIP, err)
	}

	err = v.vxlanIface.AddFDB(remoteIP, "00:00:00:00:00:00")

	if err != nil {
		return endpointInfo.UseIP, fmt.Errorf("failed to add remoteIP %q to the forwarding database", remoteIP)
	}

	var ipAddress net.IP

	cniIface, err := cni.Discover(v.localCluster.Spec.ClusterCIDR[0])
	if err == nil {
		ipAddress = net.ParseIP(cniIface.IPAddress)
	} else {
		klog.Errorf("Failed to get the CNI interface IP for cluster CIDR %q, host-networking use-cases may not work",
			v.localCluster.Spec.ClusterCIDR[0])
		ipAddress = nil
	}

	err = v.vxlanIface.AddRoute(allowedIPs, remoteVtepIP, ipAddress)

	if err != nil {
		return endpointInfo.UseIP, fmt.Errorf("failed to add route for the CIDR %q with remoteVtepIP %q and vxlanInterfaceIP %q: %w",
			allowedIPs, remoteVtepIP, v.vxlanIface.vtepIP, err)
	}

	v.connections = append(v.connections, v1.Connection{
		Endpoint: remoteEndpoint.Spec, Status: v1.Connected,
		UsingIP: endpointInfo.UseIP, UsingNAT: endpointInfo.UseNAT,
	})

	klog.V(log.DEBUG).Infof("Done adding endpoint for cluster %s", remoteEndpoint.Spec.ClusterID)

	return endpointInfo.UseIP, nil
}

func (v *vxlan) DisconnectFromEndpoint(remoteEndpoint *types.SubmarinerEndpoint) error {
	// We'll panic if remoteEndpoint is nil, this is intentional
	klog.V(log.DEBUG).Infof("Removing endpoint %#v", remoteEndpoint)

	if v.localEndpoint.Spec.ClusterID == remoteEndpoint.Spec.ClusterID {
		klog.V(log.DEBUG).Infof("Will not disconnect self")
		return nil
	}

	v.mutex.Lock()
	defer v.mutex.Unlock()

	var ip string

	for i := range v.connections {
		if v.connections[i].Endpoint.CableName == remoteEndpoint.Spec.CableName {
			ip = v.connections[i].UsingIP
		}
	}

	if ip == "" {
		klog.Errorf("Cannot disconnect remote endpoint %q - no prior connection entry found", remoteEndpoint.Spec.CableName)
		return nil
	}

	remoteIP := net.ParseIP(ip)

	if remoteIP == nil {
		return fmt.Errorf("failed to parse remote IP %s", ip)
	}

	allowedIPs := parseSubnets(remoteEndpoint.Spec.Subnets)

	err := v.vxlanIface.DelFDB(remoteIP, "00:00:00:00:00:00")
	if err != nil {
		return fmt.Errorf("failed to delete remoteIP %q from the forwarding database: %w", remoteIP, err)
	}

	err = v.vxlanIface.DelRoute(allowedIPs)

	if err != nil {
		return fmt.Errorf("failed to remove route for the CIDR %q: %w", allowedIPs, err)
	}

	v.connections = removeConnectionForEndpoint(v.connections, remoteEndpoint)
	cable.RecordDisconnected(CableDriverName, &v.localEndpoint.Spec, &remoteEndpoint.Spec)

	klog.V(log.DEBUG).Infof("Done removing endpoint for cluster %s", remoteEndpoint.Spec.ClusterID)

	return nil
}

func removeConnectionForEndpoint(connections []v1.Connection, endpoint *types.SubmarinerEndpoint) []v1.Connection {
	for j := range connections {
		if connections[j].Endpoint.CableName == endpoint.Spec.CableName {
			copy(connections[j:], connections[j+1:])
			return connections[:len(connections)-1]
		}
	}

	return connections
}

func (v *vxlan) GetConnections() ([]v1.Connection, error) {
	return v.connections, nil
}

func (v *vxlan) GetActiveConnections() ([]v1.Connection, error) {
	return v.connections, nil
}

func (v *vxlanIface) configureIPAddress(ipAddress net.IP, mask net.IPMask) error {
	ipConfig := &netlink.Addr{IPNet: &net.IPNet{
		IP:   ipAddress,
		Mask: mask,
	}}

	err := netlink.AddrAdd(v.link, ipConfig)
	if errors.Is(err, syscall.EEXIST) {
		return nil
	} else if err != nil {
		return errors.Wrapf(err, "unable to configure address (%s) on vxlan interface (%s)", ipAddress, v.link.Name)
	}

	return nil
}

func (v *vxlanIface) AddFDB(ipAddress net.IP, hwAddr string) error {
	macAddr, err := net.ParseMAC(hwAddr)
	if err != nil {
		return errors.Wrapf(err, "invalid MAC Address (%s) supplied", hwAddr)
	}

	if ipAddress == nil {
		return fmt.Errorf("invalid ipAddress (%v) supplied", ipAddress)
	}

	neigh := &netlink.Neigh{
		LinkIndex:    v.link.Index,
		Family:       unix.AF_BRIDGE,
		Flags:        netlink.NTF_SELF,
		Type:         netlink.NDA_DST,
		IP:           ipAddress,
		State:        netlink.NUD_PERMANENT | netlink.NUD_NOARP,
		HardwareAddr: macAddr,
	}

	err = netlink.NeighAppend(neigh)
	if err != nil {
		return errors.Wrapf(err, "unable to add the bridge fdb entry %v", neigh)
	}

	klog.V(log.DEBUG).Infof("Successfully added the bridge fdb entry %v", neigh)

	return nil
}

func (v *vxlanIface) DelFDB(ipAddress net.IP, hwAddr string) error {
	macAddr, err := net.ParseMAC(hwAddr)
	if err != nil {
		return errors.Wrapf(err, "invalid MAC Address (%s) supplied", hwAddr)
	}

	neigh := &netlink.Neigh{
		LinkIndex:    v.link.Index,
		Family:       unix.AF_BRIDGE,
		Flags:        netlink.NTF_SELF,
		Type:         netlink.NDA_DST,
		IP:           ipAddress,
		State:        netlink.NUD_PERMANENT | netlink.NUD_NOARP,
		HardwareAddr: macAddr,
	}

	err = netlink.NeighDel(neigh)
	if err != nil {
		return errors.Wrapf(err, "unable to delete the bridge fdb entry %v", neigh)
	}

	klog.V(log.DEBUG).Infof("Successfully deleted the bridge fdb entry %v", neigh)

	return nil
}

func (v *vxlanIface) AddRoute(ipAddressList []net.IPNet, gwIP, ip net.IP) error {
	for i := range ipAddressList {
		route := &netlink.Route{
			LinkIndex: v.link.Index,
			Src:       ip,
			Dst:       &ipAddressList[i],
			Gw:        gwIP,
			Type:      netlink.NDA_DST,
			Flags:     netlink.NTF_SELF,
			Priority:  100,
			Table:     TableID,
		}
		err := netlink.RouteAdd(route)

		if errors.Is(err, syscall.EEXIST) {
			err = netlink.RouteReplace(route)
		}

		if err != nil {
			return errors.Wrapf(err, "unable to add the route entry %v", route)
		}

		klog.V(log.DEBUG).Infof("Successfully added the route entry %v and gw ip %v", route, gwIP)
	}

	return nil
}

func (v *vxlanIface) DelRoute(ipAddressList []net.IPNet) error {
	for i := range ipAddressList {
		route := &netlink.Route{
			LinkIndex: v.link.Index,
			Dst:       &ipAddressList[i],
			Gw:        nil,
			Type:      netlink.NDA_DST,
			Flags:     netlink.NTF_SELF,
			Priority:  100,
			Table:     TableID,
		}

		err := netlink.RouteDel(route)
		if err != nil {
			return errors.Wrapf(err, "unable to add the route entry %v", route)
		}

		klog.V(log.DEBUG).Infof("Successfully deleted the route entry %v", route)
	}

	return nil
}

func (v *vxlan) Init() error {
	return nil
}

func (v *vxlan) GetName() string {
	return CableDriverName
}

// Parse CIDR string and skip errors.
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
