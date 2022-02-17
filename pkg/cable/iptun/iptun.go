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
	"sync"
	"syscall"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cable/cableutils"
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
)

type Operation int

const (
	Add Operation = iota
	Delete
	Flush
)

type iptun struct {
	localEndpoint types.SubmarinerEndpoint
	localCluster  types.SubmarinerCluster
	connections   []v1.Connection
	mutex         sync.Mutex
	ipTunIface    *ipTunIface
	netLink       netlinkAPI.Interface
}

type ipTunIface struct {
	activeEndpointHostname string
	link                   *netlink.Iptun
	iptunIP                net.IP
}

type iptunAttributes struct {
	name     string
	pmtuDisc uint8
	local    net.IP
	remote   net.IP
	mtu      int
}

func init() {
	cable.AddDriver(CableDriverName, NewDriver)
}

func NewDriver(localEndpoint *types.SubmarinerEndpoint, localCluster *types.SubmarinerCluster) (cable.Driver, error) {
	// We'll panic if localEndpoint or localCluster are nil, this is intentional
	ipip := iptun{
		localEndpoint: *localEndpoint,
		netLink:       netlinkAPI.New(),
		localCluster:  *localCluster,
	}

	// Don't create an ipip-tunnel interface till you have the remote endpoint info.
	return &ipip, nil
}

func (ipip *iptun) createIPTunInterface(remoteIP net.IP) error {
	ipAddr := ipip.localEndpoint.Spec.PrivateIP

	iptunIP, err := cableutils.GetTunnelEndPointIPAddress(ipAddr, IPTunNetworkPrefix)
	if err != nil {
		return errors.Wrapf(err, "failed to derive the iptun IP for %s", ipAddr)
	}

	defaultHostIface, err := netlinkAPI.GetDefaultGatewayInterface()
	if err != nil {
		return errors.Wrapf(err, "Unable to find the default interface on host: %s",
			ipip.localEndpoint.Spec.Hostname)
	}

	// Derive the MTU based on the default outgoing interface.
	iptunMtu := defaultHostIface.MTU - IPTunOverhead

	attrs := &iptunAttributes{
		name:     IPTunIface,
		local:    net.ParseIP(ipAddr),
		remote:   remoteIP,
		mtu:      iptunMtu,
		pmtuDisc: 1,
	}

	ipip.ipTunIface, err = newIPTunIface(attrs, ipip.localEndpoint.Spec.Hostname)
	if err != nil {
		return errors.Wrap(err, "failed to create iptun interface on Gateway Node")
	}

	ipip.ipTunIface.iptunIP = iptunIP

	err = ipip.addIPRule()
	if err != nil && !os.IsExist(err) {
		return errors.Wrap(err, "failed to add ip rule")
	}

	err = cableutils.ConfigureIPAddress(iptunIP, net.CIDRMask(8, 32), ipip.ipTunIface.link, ipip.ipTunIface.link.Name)
	if err != nil {
		return errors.Wrap(err, "failed to configure iptun interface ipaddress on the Gateway Node")
	}

	return nil
}

func newIPTunIface(attrs *iptunAttributes, activeEndPoint string) (*ipTunIface, error) {
	iface := &netlink.Iptun{
		LinkAttrs: netlink.LinkAttrs{
			Name:  attrs.name,
			MTU:   attrs.mtu,
			Flags: net.FlagUp,
		},
		Local:  attrs.local,
		Remote: attrs.remote,
	}

	ipTunIface := &ipTunIface{
		link:                   iface,
		activeEndpointHostname: activeEndPoint,
	}

	if err := createIPTunIface(ipTunIface); err != nil {
		return nil, err
	}

	return ipTunIface, nil
}

func createIPTunIface(iface *ipTunIface) error {
	err := netlink.LinkAdd(iface.link)
	if errors.Is(err, syscall.EEXIST) {
		klog.Errorf("Got error: %v, %v", err, iface.link)
		// Get the properties of existing iptun interface.
		existing, err := netlink.LinkByName(iface.link.Name)
		if err != nil {
			klog.Errorf("FAILED TO RETRIEVE LINK INFO")
			return errors.Wrap(err, "failed to retrieve link info")
		}

		if isIPtunIfaceConfigTheSame(iface.link, existing) {
			klog.V(log.DEBUG).Infof("iptun interface already exists with same configuration.")
			klog.Errorf("iptun interface already exists with same configuration.")

			iface.link = existing.(*netlink.Iptun)

			return nil
		}

		// Config does not match, delete the existing interface and re-create it.
		if err = netlink.LinkDel(existing); err != nil {
			return errors.Wrap(err, "failed to delete the existing iptun interface")
		}

		if err = netlink.LinkAdd(iface.link); err != nil {
			return errors.Wrap(err, "failed to re-create the the iptun interface")
		}
	} else if err != nil {
		return errors.Wrap(err, "failed to create the the iptun interface")
	}

	klog.Info("SUCCESSFULLY CREATED IP TUN DEVICE")

	return nil
}

func isIPtunIfaceConfigTheSame(newLink, currentLink netlink.Link) bool {
	required := newLink.(*netlink.Iptun)
	existing := currentLink.(*netlink.Iptun)

	if len(required.Local) > 0 && len(existing.Local) > 0 && !required.Local.Equal(existing.Local) {
		klog.Warningf("iptun Local (%v) of existing interface does not match with required Group (%v)", existing.Local, required.Local)
		return false
	}

	if len(required.Remote) > 0 && len(existing.Remote) > 0 && !required.Remote.Equal(existing.Remote) {
		klog.Warningf("iptun Remote (%v) of existing interface does not match with required Remote (%v)", existing.Remote, required.Remote)
		return false
	}

	return true
}

func (ipip *iptun) addIPRule() error {
	if ipip.ipTunIface != nil {
		err := cableutils.AddIPRule(TableID)
		if err != nil {
			return errors.Wrap(err, "failed to add ip rule")
		}
	}

	return nil
}

func (ipip *iptun) ConnectToEndpoint(endpointInfo *natdiscovery.NATEndpointInfo) (string, error) {
	// We'll panic if endpointInfo is nil, this is intentional
	remoteEndpoint := endpointInfo.Endpoint
	if ipip.localEndpoint.Spec.ClusterID == remoteEndpoint.Spec.ClusterID {
		klog.V(log.DEBUG).Infof("Will not connect to self")
		return "", nil
	}

	remoteIP := net.ParseIP(endpointInfo.UseIP)
	if remoteIP == nil {
		return "", fmt.Errorf("failed to parse remote IP %s", endpointInfo.UseIP)
	}

	if err := ipip.createIPTunInterface(remoteIP); err != nil {
		return "", errors.Wrap(err, "failed to setup iptun link")
	}

	allowedIPs := cableutils.ParseSubnets(remoteEndpoint.Spec.Subnets)

	klog.V(log.DEBUG).Infof("Connecting cluster %s endpoint %s",
		remoteEndpoint.Spec.ClusterID, remoteIP)
	ipip.mutex.Lock()
	defer ipip.mutex.Unlock()

	cable.RecordConnection(CableDriverName, &ipip.localEndpoint.Spec, &remoteEndpoint.Spec, string(v1.Connected), true)

	privateIP := endpointInfo.Endpoint.Spec.PrivateIP

	remoteIPTunIP, err := cableutils.GetTunnelEndPointIPAddress(privateIP, IPTunNetworkPrefix)
	if err != nil {
		return endpointInfo.UseIP, fmt.Errorf("failed to derive the iptun iptunIP for %s: %w", privateIP, err)
	}

	var ipAddress net.IP

	cniIface, err := cni.Discover(ipip.localCluster.Spec.ClusterCIDR[0])
	if err == nil {
		ipAddress = net.ParseIP(cniIface.IPAddress)
	} else {
		klog.Errorf("Failed to get the CNI interface IP for cluster CIDR %q, host-networking use-cases may not work",
			ipip.localCluster.Spec.ClusterCIDR[0])
		ipAddress = nil
	}

	err = cableutils.AddRoute(allowedIPs, remoteIPTunIP, ipAddress, ipip.ipTunIface.link.Index, TableID)

	if err != nil {
		return endpointInfo.UseIP, fmt.Errorf("failed to add route for the CIDR %q with remoteIP %q and iptunInterfaceIP %q: %w",
			allowedIPs, remoteIPTunIP, ipip.ipTunIface.iptunIP, err)
	}

	ipip.connections = append(ipip.connections, v1.Connection{
		Endpoint: remoteEndpoint.Spec, Status: v1.Connected,
		UsingIP: endpointInfo.UseIP, UsingNAT: endpointInfo.UseNAT,
	})

	klog.V(log.DEBUG).Infof("Done adding endpoint for cluster %s", remoteEndpoint.Spec.ClusterID)

	return endpointInfo.UseIP, nil
}

func (ipip *iptun) DisconnectFromEndpoint(remoteEndpoint *types.SubmarinerEndpoint) error {
	// We'll panic if remoteEndpoint is nil, this is intentional
	klog.V(log.DEBUG).Infof("Removing endpoint %#v", remoteEndpoint)

	if ipip.localEndpoint.Spec.ClusterID == remoteEndpoint.Spec.ClusterID {
		klog.V(log.DEBUG).Infof("Will not disconnect self")
		return nil
	}

	ipip.mutex.Lock()
	defer ipip.mutex.Unlock()

	var ip string

	for i := range ipip.connections {
		if ipip.connections[i].Endpoint.CableName == remoteEndpoint.Spec.CableName {
			ip = ipip.connections[i].UsingIP
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

	allowedIPs := cableutils.ParseSubnets(remoteEndpoint.Spec.Subnets)

	err := cableutils.DelRoute(allowedIPs, ipip.ipTunIface.link.Index, TableID)
	if err != nil {
		return fmt.Errorf("failed to remove route for the CIDR %q: %w", allowedIPs, err)
	}

	ipip.connections = cableutils.RemoveConnectionForEndpoint(ipip.connections, remoteEndpoint)
	cable.RecordDisconnected(CableDriverName, &ipip.localEndpoint.Spec, &remoteEndpoint.Spec)

	klog.V(log.DEBUG).Infof("Done removing endpoint for cluster %s", remoteEndpoint.Spec.ClusterID)

	return nil
}

func (ipip *iptun) GetConnections() ([]v1.Connection, error) {
	return ipip.connections, nil
}

func (ipip *iptun) GetActiveConnections() ([]v1.Connection, error) {
	return ipip.connections, nil
}

func (ipip *iptun) Init() error {
	return nil
}

func (ipip *iptun) GetName() string {
	return CableDriverName
}

func (ipip *iptun) Cleanup() error {
	klog.Infof("Uninstalling the iptun cable driver")

	return netlinkAPI.DeleteIfaceAndAssociatedRoutes(IPTunIface, TableID) // nolint:wrapcheck  // No need to wrap this error
}
