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
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/certificate"
	"github.com/submariner-io/admiral/pkg/log"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cni"
	submendpoint "github.com/submariner-io/submariner/pkg/endpoint"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/vxlan"
	k8snet "k8s.io/utils/net"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	VxlanIface                   = "vxlan-tunnel"
	VxlanVTepNetworkPrefixCIDR   = "241.0.0.0/8"
	VxlanVTepNetworkPrefixCIDRv6 = "fd00:100:200::/96"
	CableDriverName              = "vxlan"
	TableID                      = 100
	DefaultPort                  = 4500
)

type vxLan struct {
	localEndpoint v1.EndpointSpec
	localCluster  types.SubmarinerCluster
	connections   []v1.Connection
	mutex         sync.Mutex
	vxlanIfaces   map[k8snet.IPFamily]*vxlan.Interface
	netLink       netlinkAPI.Interface
	vtepIPs       map[k8snet.IPFamily]net.IP
}

var (
	vtepPrefixCIDRByFamily = map[k8snet.IPFamily]string{
		k8snet.IPv4: VxlanVTepNetworkPrefixCIDR,
		k8snet.IPv6: VxlanVTepNetworkPrefixCIDRv6,
	}

	logger = log.Logger{Logger: logf.Log.WithName("vxlan")}
)

func GetVxlanInterfaceName(ipFamily k8snet.IPFamily) string {
	if ipFamily == k8snet.IPv6 {
		return VxlanIface + "-6"
	}

	return VxlanIface
}

func init() {
	cable.AddDriver(CableDriverName, NewDriver)
}

func NewDriver(localEndpoint *submendpoint.Local,
	localCluster *types.SubmarinerCluster, _ certificate.SigningRequestor,
) (cable.Driver, error) {
	// We'll panic if localEndpoint or localCluster are nil, this is intentional
	var err error

	v := vxLan{
		localEndpoint: *localEndpoint.Spec(),
		netLink:       netlinkAPI.New(),
		localCluster:  *localCluster,
		vxlanIfaces:   make(map[k8snet.IPFamily]*vxlan.Interface),
		vtepIPs:       make(map[k8snet.IPFamily]net.IP),
	}

	if strings.EqualFold(v.localEndpoint.CableName, CableDriverName) && v.localEndpoint.NATEnabled {
		logger.Warning("VxLan cable-driver is supported only with no NAT deployments")
	}

	port, err := v.localEndpoint.GetBackendPort(v1.UDPPortConfig, DefaultPort)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get the UDP port configuration")
	}

	// Create VXLAN interface for each IP family supported by the endpoint
	for _, family := range v.localEndpoint.GetIPFamilies() {
		if err = v.createVxlanInterface(int(port), family); err != nil {
			return nil, errors.Wrapf(err, "failed to setup Vxlan link for IPv%v", family)
		}
	}

	return &v, nil
}

func (v *vxLan) createVxlanInterface(port int, family k8snet.IPFamily) error {
	ipAddr := v.localEndpoint.GetPrivateIP(family)

	vtepPrefixCIDR := vtepPrefixCIDRByFamily[family]

	vtepIP, err := vxlan.GetVtepIPAddressFrom(ipAddr, vtepPrefixCIDR, family)
	if err != nil {
		return errors.Wrapf(err, "failed to derive the vxlan vtepIP for %s", ipAddr)
	}

	v.vtepIPs[family] = vtepIP

	logger.V(log.DEBUG).Infof("Derived VTEP IPv%v %s from private IP %s", family, vtepIP, ipAddr)

	defaultHostIface, err := v.netLink.GetDefaultGatewayInterface(family)
	if err != nil {
		return errors.Wrapf(err, "Unable to find the default IPv%v interface on host: %s", family, v.localEndpoint.Hostname)
	}

	interfaceName := GetVxlanInterfaceName(family)
	attrs := &vxlan.Attributes{
		Name:     interfaceName,
		VxlanID:  1000,
		Group:    nil,
		SrcAddr:  nil,
		VtepPort: port,
		Mtu:      defaultHostIface.MTU(),
	}

	// For IPv6 VxLAN interface SrcAddr should be set with local IP
	if family == k8snet.IPv6 {
		localIP := net.ParseIP(ipAddr)
		attrs.SrcAddr = localIP
	}

	vxlanIface, err := vxlan.NewInterface(attrs, v.netLink)
	if err != nil {
		return errors.Wrapf(err, "failed to create vxlan interface %s on Gateway Node", interfaceName)
	}

	v.vxlanIfaces[family] = vxlanIface

	err = vxlanIface.SetupLink()
	if err != nil {
		return errors.Wrapf(err, "failed to setup link for vxlan interface %s", interfaceName)
	}

	err = v.netLink.RuleAddIfNotPresent(netlinkAPI.NewTableRule(TableID, family))
	if err != nil {
		return errors.Wrapf(err, "failed to add IPv%v ip rule", family)
	}

	err = v.netLink.EnsureLooseModeIsConfigured(interfaceName, family)
	if err != nil {
		return errors.Wrapf(err, "error while validating loose mode for IPv%v", family)
	}

	logger.V(log.DEBUG).Infof("Successfully configured rp_filter to loose mode(2) on %s for IPv%v", interfaceName, family)

	_, ipNet, err := net.ParseCIDR(vtepPrefixCIDR)
	if err != nil {
		return errors.Wrapf(err, "invalid VTEP CIDR %q", vtepPrefixCIDR)
	}

	err = vxlanIface.ConfigureIPAddress(vtepIP, ipNet.Mask)
	if err != nil {
		return errors.Wrapf(err, "failed to configure vxlan interface ipaddress on %s", interfaceName)
	}

	err = v.netLink.EnableForwarding(interfaceName, family)
	if err != nil {
		return errors.Wrapf(err, "error enabling forwarding on the %q iface for IPv%v", interfaceName, family)
	}

	logger.V(log.DEBUG).Infof("Successfully configured VXLAN interface for IPv%v with VTEP IP %s", family, vtepIP)

	return nil
}

func (v *vxLan) ConnectToEndpoint(endpointInfo *natdiscovery.NATEndpointInfo) (string, error) {
	// We'll panic if endpointInfo is nil, this is intentional
	remoteEndpoint := endpointInfo.Endpoint
	if v.localEndpoint.ClusterID == remoteEndpoint.Spec.ClusterID {
		logger.V(log.DEBUG).Infof("Will not connect to self")
		return "", nil
	}

	family := endpointInfo.UseFamily
	remoteIP := net.ParseIP(endpointInfo.UseIP)

	if remoteIP == nil {
		return "", fmt.Errorf("failed to parse remote IP %s", endpointInfo.UseIP)
	}

	allowedIPs := remoteEndpoint.Spec.ParseSubnets(family)

	logger.V(log.DEBUG).Infof("Connecting cluster %s endpoint %s for IPv%v",
		remoteEndpoint.Spec.ClusterID, remoteIP, family)

	v.mutex.Lock()
	defer v.mutex.Unlock()

	vxlanIface, exists := v.vxlanIfaces[family]
	if !exists {
		return "", fmt.Errorf("no VXLAN interface configured for IPv%v", family)
	}

	cable.RecordConnection(CableDriverName, &v.localEndpoint, &remoteEndpoint.Spec, string(v1.Connected), true, family)

	privateIP := endpointInfo.Endpoint.Spec.GetPrivateIP(family)
	vtepPrefixCIDR := vtepPrefixCIDRByFamily[family]

	remoteVtepIP, err := vxlan.GetVtepIPAddressFrom(privateIP, vtepPrefixCIDR, family)
	if err != nil {
		return endpointInfo.UseIP, fmt.Errorf("failed to derive the vxlan vtepIP for %s: %w", privateIP, err)
	}

	err = vxlanIface.AddFDB(remoteIP, "00:00:00:00:00:00")
	if err != nil {
		return endpointInfo.UseIP, fmt.Errorf("failed to add remoteIP %q to the forwarding database: %w", remoteIP, err)
	}

	var ipAddress net.IP

	cniIface, err := cni.Discover(v.localCluster.Spec.ClusterCIDR, family)
	if err == nil {
		ipAddress = net.ParseIP(cniIface.IPAddress)
	} else {
		logger.Errorf(nil, "Failed to get the CNI interface IP for cluster CIDR %q, host-networking use-cases may not work",
			v.localCluster.Spec.ClusterCIDR[0])
	}

	err = vxlanIface.AddRoutes(remoteVtepIP, ipAddress, TableID, allowedIPs...)
	if err != nil {
		return endpointInfo.UseIP, fmt.Errorf("failed to add route for the CIDR %q with remoteVtepIP %q: %w",
			allowedIPs, remoteVtepIP, err)
	}

	v.connections = append(v.connections, v1.Connection{
		Endpoint: remoteEndpoint.Spec, Status: v1.Connected,
		UsingIP: endpointInfo.UseIP, UsingNAT: endpointInfo.UseNAT,
	})

	logger.V(log.DEBUG).Infof("Done adding IPv%v endpoint for cluster %s with VTEP %s -> %s",
		family, remoteEndpoint.Spec.ClusterID, v.vtepIPs[family], remoteVtepIP)

	return endpointInfo.UseIP, nil
}

func (v *vxLan) DisconnectFromEndpoint(remoteEndpoint *types.SubmarinerEndpoint, family k8snet.IPFamily) error {
	// We'll panic if remoteEndpoint is nil, this is intentional
	logger.V(log.DEBUG).Infof("Removing IPv%v endpoint %#v", family, remoteEndpoint)

	if v.localEndpoint.ClusterID == remoteEndpoint.Spec.ClusterID {
		logger.V(log.DEBUG).Infof("Will not disconnect self")
		return nil
	}

	v.mutex.Lock()
	defer v.mutex.Unlock()

	vxlanIface, exists := v.vxlanIfaces[family]
	if !exists {
		return fmt.Errorf("no VXLAN interface configured for IPv%v", family)
	}

	var ip string

	for i := range v.connections {
		if v.connections[i].Endpoint.CableName == remoteEndpoint.Spec.CableName && v.connections[i].GetFamily() == family {
			ip = v.connections[i].UsingIP
		}
	}

	if ip == "" {
		logger.Errorf(nil, "Cannot disconnect remote endpoint %q - no prior connection entry found", remoteEndpoint.Spec.CableName)
		return nil
	}

	remoteIP := net.ParseIP(ip)
	if remoteIP == nil {
		return fmt.Errorf("failed to parse remote IP %s", ip)
	}

	allowedIPs := remoteEndpoint.Spec.ParseSubnets(family)

	err := vxlanIface.DelFDB(remoteIP, "00:00:00:00:00:00")
	if err != nil {
		return fmt.Errorf("failed to delete remoteIP %q from the forwarding database: %w", remoteIP, err)
	}

	err = vxlanIface.DelRoutes(TableID, allowedIPs...)
	if err != nil {
		return fmt.Errorf("failed to remove route for the CIDR %q: %w", allowedIPs, err)
	}

	v.connections = removeConnectionForEndpoint(v.connections, remoteEndpoint, family)
	cable.RecordDisconnected(CableDriverName, &v.localEndpoint, &remoteEndpoint.Spec, family)

	logger.V(log.DEBUG).Infof("Done removing endpoint for cluster %s", remoteEndpoint.Spec.ClusterID)

	return nil
}

func removeConnectionForEndpoint(connections []v1.Connection, endpoint *types.SubmarinerEndpoint, family k8snet.IPFamily) []v1.Connection {
	for j := range connections {
		if connections[j].Endpoint.CableName == endpoint.Spec.CableName && connections[j].GetFamily() == family {
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

func (v *vxLan) Init(_ context.Context) error {
	return nil
}

func (v *vxLan) GetName() string {
	return CableDriverName
}

func (v *vxLan) Cleanup(_ context.Context) error {
	logger.Infof("Uninstalling the vxlan cable driver")

	// Clean up rules for all configured families
	families := v.localEndpoint.GetIPFamilies()
	if len(families) == 0 {
		families = []k8snet.IPFamily{k8snet.IPv4}
	}

	for _, family := range families {
		interfaceName := GetVxlanInterfaceName(family)

		err := netlinkAPI.DeleteIfaceAndAssociatedRoutes(interfaceName, TableID, family)
		if err != nil {
			logger.Errorf(nil, "Unable to delete interface %s and associated routes from table %d", interfaceName, TableID)
		}

		err = v.netLink.RuleDelIfPresent(netlinkAPI.NewTableRule(TableID, family))
		if err != nil {
			logger.Errorf(err, "Unable to delete IPv%v IP rule pointing to %d table", family, TableID)
		}
	}

	return nil
}
