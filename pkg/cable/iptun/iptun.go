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

package iptun

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cidr"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/cni"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/vishvananda/netlink"
	"k8s.io/klog"
)

const (
	IPTunIface         = "ipip-tunnel"
	IPTunOverhead      = 20
	IPTunNetworkPrefix = 242
	CableDriverName    = "iptun"
	TableID            = 100
	IPTunRouteAdd      = true
	IPTunRouteDel      = false
)

type ipTun struct {
	localEndpoint types.SubmarinerEndpoint
	localCluster  types.SubmarinerCluster
	connections   []v1.Connection
	mutex         sync.Mutex
	ipTunIface    *ipTunIface
	netLink       netlinkAPI.Interface
	cniIPAddress  net.IP
}

type ipTunIface struct {
	link *netlink.Iptun
}

type ipTunAttributes struct {
	name  string
	local net.IP
	mtu   int
}

func init() {
	cable.AddDriver(CableDriverName, NewDriver)
}

func NewDriver(localEndpoint *types.SubmarinerEndpoint, localCluster *types.SubmarinerCluster) (cable.Driver, error) {
	// We'll panic if localEndpoint or localCluster are nil, this is intentional
	ipip := ipTun{
		localEndpoint: *localEndpoint,
		netLink:       netlinkAPI.New(),
		localCluster:  *localCluster,
	}

	if err := ipip.createIPTunInterface(); err != nil {
		return nil, errors.Wrap(err, "failed to setup ipTun link")
	}

	cniIface, err := cni.Discover(localCluster.Spec.ClusterCIDR[0])
	if err == nil {
		ipip.cniIPAddress = net.ParseIP(cniIface.IPAddress)
	} else {
		klog.V(log.DEBUG).Infof("Failed to get the CNI interface IP for cluster CIDR %q, host-networking use-cases may not work",
			ipip.localCluster.Spec.ClusterCIDR[0])
		ipip.cniIPAddress = nil
	}

	return &ipip, nil
}

func (i *ipTun) createIPTunInterface() error {
	ipAddr := i.localEndpoint.Spec.PrivateIP

	ipTunIP, err := getTunnelEndPointIPAddress(ipAddr, IPTunNetworkPrefix)
	if err != nil {
		return errors.Wrapf(err, "failed to derive the ipTun IP for %s", ipAddr)
	}

	defaultHostIface, err := netlinkAPI.GetDefaultGatewayInterface()
	if err != nil {
		return errors.Wrapf(err, "Unable to find the default interface on host: %s",
			i.localEndpoint.Spec.Hostname)
	}

	// Derive the MTU based on the default outgoing interface.
	ipTunMtu := defaultHostIface.MTU - IPTunOverhead

	attrs := &ipTunAttributes{
		name:  IPTunIface,
		local: net.ParseIP(ipAddr),
		mtu:   ipTunMtu,
	}

	i.ipTunIface, err = newIPTunIface(attrs)
	if err != nil {
		return errors.Wrap(err, "failed to create ipTun interface on Gateway Node")
	}

	rule := netlink.NewRule()
	rule.Table = TableID
	rule.Priority = TableID

	err = i.netLink.RuleAddIfNotPresent(rule)
	if err != nil {
		return errors.Wrap(err, "failed to add ip rule")
	}

	ipConfig := &netlink.Addr{IPNet: &net.IPNet{
		IP:   ipTunIP,
		Mask: net.CIDRMask(8, 32),
	}}

	err = i.netLink.AddrAddIfNotPresent(i.ipTunIface.link, ipConfig)
	if err != nil {
		if os.IsExist(err) {
			klog.V(log.DEBUG).Infof("Requested ipTun interface ipaddress already configured on the Gateway Node")
		} else {
			return errors.Wrap(err, "Error configuring the ipTun interface IP address")
		}
	}

	return nil
}

func newIPTunIface(attrs *ipTunAttributes) (*ipTunIface, error) {
	iface := &netlink.Iptun{
		LinkAttrs: netlink.LinkAttrs{
			Name:  attrs.name,
			MTU:   attrs.mtu,
			Flags: net.FlagUp,
		},
		Local: attrs.local,
	}

	ipTunIface := &ipTunIface{
		link: iface,
	}

	if err := createIPTunIface(ipTunIface); err != nil {
		return nil, err
	}

	return ipTunIface, nil
}

func createIPTunIface(iface *ipTunIface) error {
	err := netlink.LinkAdd(iface.link)
	if errors.Is(err, syscall.EEXIST) {
		// Get the properties of existing ipTun interface.
		existing, err := netlink.LinkByName(iface.link.Name)
		if err != nil {
			return errors.Wrap(err, "failed to retrieve link info")
		}

		if isIPtunIfaceConfigTheSame(iface.link, existing) {
			klog.V(log.DEBUG).Infof("ipTun interface already exists with same configuration. No need to recreate")

			iface.link = existing.(*netlink.Iptun)

			return nil
		}

		// Config does not match, delete the existing interface and re-create it.
		if err = netlink.LinkDel(existing); err != nil {
			return errors.Wrap(err, "failed to delete the existing ipTun interface")
		}

		if err = netlink.LinkAdd(iface.link); err != nil {
			return errors.Wrap(err, "failed to re-create the the ipTun interface")
		}
	}

	if err != nil {
		return errors.Wrap(err, "failed to create the the ipTun interface")
	}

	klog.Info("Successfully created IP in IP interface")

	return nil
}

func isIPtunIfaceConfigTheSame(newLink, currentLink netlink.Link) bool {
	required := newLink.(*netlink.Iptun)
	existing := currentLink.(*netlink.Iptun)

	if len(required.Local) > 0 && len(existing.Local) > 0 && !required.Local.Equal(existing.Local) {
		klog.Warningf("ipTun Local (%v) of existing interface does not match with required Group (%v)", existing.Local, required.Local)
		return false
	}

	if len(required.Remote) > 0 && len(existing.Remote) > 0 && !required.Remote.Equal(existing.Remote) {
		klog.Warningf("ipTun Remote (%v) of existing interface does not match with required Remote (%v)", existing.Remote, required.Remote)
		return false
	}

	return true
}

func (i *ipTun) ConnectToEndpoint(endpointInfo *natdiscovery.NATEndpointInfo) (string, error) {
	// We'll panic if endpointInfo is nil, this is intentional
	remoteEndpoint := endpointInfo.Endpoint
	if i.localEndpoint.Spec.ClusterID == remoteEndpoint.Spec.ClusterID {
		klog.V(log.DEBUG).Infof("Will not connect to self")
		return "", nil
	}

	remoteIP := net.ParseIP(endpointInfo.UseIP)
	if remoteIP == nil {
		return "", fmt.Errorf("failed to parse remote IP %s", endpointInfo.UseIP)
	}

	allowedIPs := cidr.ParseSubnets(remoteEndpoint.Spec.Subnets)

	klog.V(log.DEBUG).Infof("Connecting cluster %s endpoint %s",
		remoteEndpoint.Spec.ClusterID, remoteIP)
	i.mutex.Lock()
	defer i.mutex.Unlock()

	cable.RecordConnection(CableDriverName, &i.localEndpoint.Spec, &remoteEndpoint.Spec, string(v1.Connected), true)

	privateIP := endpointInfo.Endpoint.Spec.PrivateIP

	remoteIPTunIP, err := getTunnelEndPointIPAddress(privateIP, IPTunNetworkPrefix)
	if err != nil {
		return "", errors.Wrapf(err, "failed to derive the ipTun IP for %s: ", privateIP)
	}

	err = addDelRouteEncap(IPTunRouteAdd, allowedIPs, remoteIPTunIP, remoteIP, i)
	if err != nil {
		return "", errors.Wrapf(err, "failed to add route for the CIDR %q with remoteIP %q and ipTun Interface IP %q: ",
			allowedIPs, remoteIPTunIP, i.ipTunIface.link.Local)
	}

	i.connections = append(i.connections, v1.Connection{
		Endpoint: remoteEndpoint.Spec, Status: v1.Connected,
		UsingIP: endpointInfo.UseIP, UsingNAT: endpointInfo.UseNAT,
	})

	klog.V(log.DEBUG).Infof("Done adding endpoint for cluster %s", remoteEndpoint.Spec.ClusterID)

	return endpointInfo.UseIP, nil
}

func addDelRouteEncap(add bool, ipAddressList []net.IPNet, remoteTunIP, remoteIP net.IP, iface *ipTun) error {
	link := iface.ipTunIface.link

	for i := range ipAddressList {
		exists, err := checkEncapRoute(ipAddressList[i], remoteTunIP)
		if err != nil {
			return errors.Wrap(err, "unable to check the route entry")
		}

		args := []string{}
		args = append(args, "route")

		if add {
			switch exists {
			case true:
				args = append(args, "replace")
			default:
				args = append(args, "add")
			}
		} else {
			switch exists {
			case true:
				args = append(args, "delete")
			default:
				return errors.Wrap(err, "unable to delete the route entry as it was not found")
			}
		}

		args = append(args, ipAddressList[i].String(),
			"encap", "ip", "id", "100",
			"dst", remoteIP.String(),
			"via", remoteTunIP.String(),
			"dev", link.Attrs().Name,
			"table", strconv.Itoa(TableID),
			"metric", strconv.Itoa(100))

		if iface.cniIPAddress != nil {
			args = append(args, "src", iface.cniIPAddress.String())
		}

		if err := ipCommand(args...); err != nil {
			return errors.Wrapf(err, "error adding/deleting route encap with with args %v: ", args)
		}
	}

	return nil
}

func ipCommand(args ...string) error {
	var err error

	for i := 0; i < 3; i++ {
		cmd := exec.Command("ip", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err = cmd.Run(); err == nil {
			break
		}

		klog.Warningf("error %v error running the ip command with args: %v", err, args)
		time.Sleep(1 * time.Second)
	}

	if err != nil {
		return errors.Wrapf(err, "error running the ip command with args %v", args)
	}

	return nil
}

func checkEncapRoute(ip net.IPNet, remoteTunIP net.IP) (bool, error) {
	routes, err := netlink.RouteGet(ip.IP)
	if err != nil {
		return false, errors.Wrapf(err, "Unable to check routes, error retrieving routes for Addr %v", ip.String())
	}

	for i := range routes {
		if routes[i].Gw.Equal(remoteTunIP) {
			// Found an IPinIP Tunnel end point route
			return true, nil
		}
	}

	return false, nil
}

func (i *ipTun) DisconnectFromEndpoint(remoteEndpoint *types.SubmarinerEndpoint) error {
	// We'll panic if remoteEndpoint is nil, this is intentional
	klog.V(log.DEBUG).Infof("Removing endpoint %#v", remoteEndpoint)

	if i.localEndpoint.Spec.ClusterID == remoteEndpoint.Spec.ClusterID {
		klog.V(log.DEBUG).Infof("Will not disconnect self")
		return nil
	}

	i.mutex.Lock()
	defer i.mutex.Unlock()

	var ip string

	for j := range i.connections {
		if i.connections[j].Endpoint.CableName == remoteEndpoint.Spec.CableName {
			ip = i.connections[j].UsingIP
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

	allowedIPs := cidr.ParseSubnets(remoteEndpoint.Spec.Subnets)

	privateIP := remoteEndpoint.Spec.PrivateIP

	remoteIPTunIP, err := getTunnelEndPointIPAddress(privateIP, IPTunNetworkPrefix)
	if err != nil {
		return errors.Wrapf(err, "failed to derive the ipTun IP for %s: ", privateIP)
	}

	err = addDelRouteEncap(IPTunRouteDel, allowedIPs, remoteIPTunIP, remoteIP, i)
	if err != nil {
		return errors.Wrapf(err, "failed to remove route for the CIDR %q: ", allowedIPs)
	}

	i.connections = v1.RemoveConnectionForEndpoint(i.connections, remoteEndpoint.Spec.CableName)
	cable.RecordDisconnected(CableDriverName, &i.localEndpoint.Spec, &remoteEndpoint.Spec)

	klog.V(log.DEBUG).Infof("Done removing endpoint for cluster %s", remoteEndpoint.Spec.ClusterID)

	return nil
}

func getTunnelEndPointIPAddress(ipAddr string, prefix int) (net.IP, error) {
	ipSlice := strings.Split(ipAddr, ".")
	if len(ipSlice) < 4 {
		return nil, fmt.Errorf("invalid ipAddr [%s]", ipAddr)
	}

	ipSlice[0] = strconv.Itoa(prefix)
	tunnelIP := net.ParseIP(strings.Join(ipSlice, "."))

	return tunnelIP, nil
}

func (i *ipTun) GetConnections() ([]v1.Connection, error) {
	return i.connections, nil
}

func (i *ipTun) GetActiveConnections() ([]v1.Connection, error) {
	return i.connections, nil
}

func (i *ipTun) Init() error {
	return nil
}

func (i *ipTun) GetName() string {
	return CableDriverName
}

func (i *ipTun) Cleanup() error {
	klog.Infof("Uninstalling the ipTun cable driver")

	return netlinkAPI.DeleteIfaceAndAssociatedRoutes(IPTunIface, TableID) // nolint:wrapcheck  // No need to wrap this error
}
