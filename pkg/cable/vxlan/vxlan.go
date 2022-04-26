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
	"github.com/submariner-io/admiral/pkg/stringset"
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
	link   *netlink.Vxlan
	vtepIP net.IP
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

	if err = v.createVxlanInterface(int(port)); err != nil {
		return nil, errors.Wrap(err, "failed to setup Vxlan link")
	}

	return &v, nil
}

// This setup up vx-lan interface between gateway nodes of clusters.
func (v *vxlan) createVxlanInterface(port int) error {
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

	v.vxlanIface, err = newVxlanIface(attrs)
	if err != nil {
		return errors.Wrap(err, "failed to create vxlan interface on Gateway Node")
	}

	v.vxlanIface.vtepIP = vtepIP

	err = v.netLink.RuleAddIfNotPresent(netlinkAPI.NewTableRule(TableID))
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

func newVxlanIface(attrs *vxlanAttributes) (*vxlanIface, error) {
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
		link: iface,
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

func (v *vxlan) ConnectToEndpoint(endpointInfo *natdiscovery.NATEndpointInfo) (string, error) { // nolint // Supposed to be complicated

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

	// Build map to form the nexthops behind each remote endpoint
	remoteSubnetToGWs := map[string]stringset.Interface{}
	healthcheckMap := map[string]net.IP{}

	// add the Vtep IP of the new GW
	remoteVtepIP, err := v.getVxlanVtepIPAddress(endpointInfo.Endpoint.Spec.PrivateIP)
	if err != nil {
		return endpointInfo.UseIP, fmt.Errorf("failed to derive the vxlan vtepIP for %s: %w", endpointInfo.Endpoint.Spec.PrivateIP, err)
	}

	for _, subnet := range remoteEndpoint.Spec.Subnets {
		remoteSubnetToGWs[subnet] = stringset.NewSynchronized()
		// These are the nexthops for each subnet
		remoteSubnetToGWs[subnet].Add(remoteVtepIP.String())
	}

	// add the VTEP IP of existing GWs
	for _, connection := range v.connections { // nolint:gocritic
		// add the Vtep IP of the new GW
		gwVtepIP, err := v.getVxlanVtepIPAddress(connection.Endpoint.PrivateIP)
		if err != nil {
			return "", fmt.Errorf("failed to derive the vxlan vtepIP for %s: %w", endpointInfo.Endpoint.Spec.PrivateIP, err)
		}
		// only add nexthops for correct subnets and skip the connection if it does
		// not represent the desired remote subnet
		skipConnection := func() bool {
			for _, subnet := range connection.Endpoint.Subnets {
				if _, ok := remoteSubnetToGWs[subnet]; ok {
					klog.V(log.DEBUG).Infof("Adding gwVtepIP %s for subnet %s", gwVtepIP.String(), subnet)
					remoteSubnetToGWs[subnet].Add(gwVtepIP.String())

					continue
				}

				return true
			}

			return false
		}

		if skipConnection() {
			klog.V(log.DEBUG).Infof("Skipping connection %s since it does not represent %v",
				connection.Endpoint.CableName, remoteEndpoint.Spec.Subnets)
			continue
		}

		if connection.Endpoint.HealthCheckIP != "" {
			healthcheckMap[connection.Endpoint.HealthCheckIP] = gwVtepIP
		}
	}

	// This will get pick up in later endpoint event if not assigned a globalIP yet from globalnet-controller
	if endpointInfo.Endpoint.Spec.HealthCheckIP != "" {
		healthcheckMap[endpointInfo.Endpoint.Spec.HealthCheckIP] = remoteVtepIP
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

	// here we need to add Multipath routing for each endpoint
	klog.V(log.DEBUG).Infof("Reconciling Inter Cluster Routes for Remote GWs %#v", remoteSubnetToGWs)

	err = v.vxlanIface.reconcileInterClusterRoutes(remoteSubnetToGWs, ipAddress)
	if err != nil {
		return endpointInfo.UseIP, fmt.Errorf("failed to add route for the CIDR %q with remoteVtepIP %q and vxlanInterfaceIP %q: %w",
			allowedIPs, remoteVtepIP, v.vxlanIface.vtepIP, err)
	}

	klog.V(log.DEBUG).Infof("Reconciling Inter Cluster Routes for healthcheck IPs %#v", healthcheckMap)

	// Add routes for the multiple GW healthcheck IPs
	err = v.vxlanIface.addRoutesForHealthcheck(healthcheckMap)
	if err != nil {
		return "", fmt.Errorf("failed to create routes for healthcheckMap %v: %w",
			healthcheckMap, err)
	}

	v.connections = append(v.connections, v1.Connection{
		Endpoint: remoteEndpoint.Spec, Status: v1.Connected,
		UsingIP: endpointInfo.UseIP, UsingNAT: endpointInfo.UseNAT,
	})

	klog.V(log.DEBUG).Infof("Done adding endpoint for cluster %s", remoteEndpoint.Spec.ClusterID)

	return endpointInfo.UseIP, nil
}

// TODO MAG POC: make sure we cleanup correctly here.
func (v *vxlan) DisconnectFromEndpoint(remoteEndpoint *types.SubmarinerEndpoint) error {
	// We'll panic if remoteEndpoint is nil, this is intentional
	klog.V(log.DEBUG).Infof("Removing endpoint %#v", remoteEndpoint)

	// add the Vtep IP of the new GW
	gwVtepIP, err := v.getVxlanVtepIPAddress(remoteEndpoint.Spec.PrivateIP)
	if err != nil {
		return fmt.Errorf("failed to derive the vxlan vtepIP for %s: %w", remoteEndpoint.Spec.PrivateIP, err)
	}

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

	err = v.vxlanIface.DelFDB(remoteIP, "00:00:00:00:00:00")
	if err != nil {
		return fmt.Errorf("failed to delete remoteIP %q from the forwarding database: %w", remoteIP, err)
	}

	err = v.vxlanIface.DelRoute(allowedIPs)

	if err != nil {
		return fmt.Errorf("failed to remove route for the CIDR %q: %w", allowedIPs, err)
	}

	err = v.vxlanIface.delRouteForHealthcheck(remoteEndpoint.Spec.HealthCheckIP, gwVtepIP)

	if err != nil {
		return fmt.Errorf("failed to remove route for the HealthcheckIP %q: %w", remoteEndpoint.Spec.HealthCheckIP, err)
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

// buildDesiredRoutes will make all routes needed for inter cluster communication within
// submariner. Specifically it will route traffic at a GW node, destined
// for another cluster, to an active GW on the next cluster.
func (v *vxlanIface) buildDesiredRoutes(remoteSubnetToGWs map[string]stringset.Interface, ip net.IP) ([]netlink.Route, error) {
	desiredRoutes := []netlink.Route{}

	for remoteSubnet, gwIPs := range remoteSubnetToGWs {
		nextHops := []*netlink.NexthopInfo{}

		// Build nexthops for a given globalnet subnet
		for _, gw := range gwIPs.Elements() {
			nextHops = append(nextHops, &netlink.NexthopInfo{
				Gw:        net.ParseIP(gw),
				LinkIndex: v.link.Attrs().Index,
			})
		}

		_, remoteNet, err := net.ParseCIDR(remoteSubnet)
		if err != nil {
			return nil, errors.Wrapf(err, "error parsing remote subnet %s", remoteSubnet)
		}

		route := &netlink.Route{
			LinkIndex: v.link.Index,
			Src:       ip,
			Dst:       remoteNet,
			MultiPath: nextHops,
			Type:      netlink.NDA_DST,
			Flags:     netlink.NTF_SELF,
			Priority:  100,
			Table:     TableID,
		}

		desiredRoutes = append(desiredRoutes, *route)
	}

	return desiredRoutes, nil
}

// Reconcile the routes based on the GWIPs installed on this device using rtnetlink.
// Eventually we should move this to a common library with the code from the RA routes_iface.go.
func (v *vxlanIface) reconcileInterClusterRoutes(remoteSubnetToGWs map[string]stringset.Interface, ip net.IP) error {
	desiredRouteList, err := v.buildDesiredRoutes(remoteSubnetToGWs, ip)
	if err != nil {
		return errors.Wrapf(err, "error retrieving desired routes for link %s", v.link.Name)
	}

	// Let's now add the routes that are missing.
	for i := range desiredRouteList { // nolint:gocritic
		err := netlink.RouteAdd(&desiredRouteList[i])

		if errors.Is(err, syscall.EEXIST) {
			err = netlink.RouteReplace(&desiredRouteList[i])
		}

		if err != nil {
			return errors.Wrapf(err, "unable to add the route entry %v", desiredRouteList[i])
		}

		klog.V(log.DEBUG).Infof("Successfully added the route entry %+v", desiredRouteList[i])
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

func (v *vxlan) Cleanup() error {
	klog.Infof("Uninstalling the vxlan cable driver")

	err := netlinkAPI.DeleteIfaceAndAssociatedRoutes(VxlanIface, TableID)
	if err != nil {
		klog.Errorf("unable to delete interface %s and associated routes from table %d", VxlanIface, TableID)
	}

	err = v.netLink.RuleDelIfPresent(netlinkAPI.NewTableRule(TableID))
	if err != nil {
		return errors.Wrapf(err, "unable to delete IP rule pointing to %d table", TableID)
	}

	return nil
}

func (v *vxlanIface) addRoutesForHealthcheck(healthcheckMap map[string]net.IP) error {
	for healthCheckIP, gwVtepIP := range healthcheckMap {
		route := &netlink.Route{
			LinkIndex: v.link.Index,
			Dst:       netlink.NewIPNet(net.ParseIP(healthCheckIP)),
			Gw:        gwVtepIP,
			Priority:  99,
			Table:     TableID,
		}
		err := netlink.RouteAdd(route)

		if errors.Is(err, syscall.EEXIST) {
			err = netlink.RouteReplace(route)
		}

		if err != nil {
			return errors.Wrapf(err, "unable to add the route entry %v", route)
		}

		klog.V(log.DEBUG).Infof("Successfully added the route entry %v", route)
	}

	return nil
}

func (v *vxlanIface) delRouteForHealthcheck(healthcheckIP string, gwVtepIP net.IP) error {
	route := &netlink.Route{
		LinkIndex: v.link.Index,
		Dst:       netlink.NewIPNet(net.ParseIP(healthcheckIP)),
		Gw:        gwVtepIP,
		Priority:  99,
		Table:     TableID,
	}

	err := netlink.RouteDel(route)
	if err != nil {
		return errors.Wrapf(err, "unable to add the route entry %v", route)
	}

	klog.V(log.DEBUG).Infof("Successfully deleted the route entry %v", route)

	return nil
}
