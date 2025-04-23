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

//nolint:wrapcheck // Most of the functions are simple wrappers so we'll let the caller wrap errors.
package netlink

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/vishvananda/netlink"
	k8snet "k8s.io/utils/net"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type Basic interface {
	LinkAdd(link netlink.Link) error
	LinkDel(link netlink.Link) error
	LinkByName(name string) (netlink.Link, error)
	LinkSetUp(link netlink.Link) error
	AddrAdd(link netlink.Link, addr *netlink.Addr) error
	AddrDel(link netlink.Link, addr *netlink.Addr) error
	AddrList(link netlink.Link, family k8snet.IPFamily) ([]netlink.Addr, error)
	AddrSubscribe(addrCh chan netlink.AddrUpdate, doneCh chan struct{}) error
	NeighAppend(neigh *netlink.Neigh) error
	NeighDel(neigh *netlink.Neigh) error
	RouteAdd(route *netlink.Route) error
	RouteDel(route *netlink.Route) error
	RouteReplace(route *netlink.Route) error
	RouteGet(destination net.IP) ([]netlink.Route, error)
	RouteList(link netlink.Link, family k8snet.IPFamily) ([]netlink.Route, error)
	FlushRouteTable(tableID int) error
	RuleAdd(rule *netlink.Rule) error
	RuleDel(rule *netlink.Rule) error
	RuleList(family k8snet.IPFamily) ([]netlink.Rule, error)
	XfrmPolicyAdd(policy *netlink.XfrmPolicy) error
	XfrmPolicyDel(policy *netlink.XfrmPolicy) error
	XfrmPolicyList(family k8snet.IPFamily) ([]netlink.XfrmPolicy, error)
	EnableLooseModeReversePathFilter(interfaceName string, family k8snet.IPFamily) error
	EnsureLooseModeIsConfigured(interfaceName string, family k8snet.IPFamily) error
	EnableForwarding(interfaceName string, family k8snet.IPFamily) error
	ConfigureTCPMTUProbe(mtuProbe, baseMss string) error
	InterfaceByName(name string) (NetworkInterface, error)
	InterfaceByIndex(index int) (NetworkInterface, error)
}

type Interface interface {
	Basic
	AddrAddIfNotPresent(link netlink.Link, addr *netlink.Addr) error
	RuleAddIfNotPresent(rule *netlink.Rule) error
	RuleDelIfPresent(rule *netlink.Rule) error
	RouteAddOrReplace(route *netlink.Route) error
	AddDestinationRoutes(destIPs []net.IPNet, gwIP, srcIP net.IP, linkIndex, tableID int) error
	DeleteDestinationRoutes(destIPs []net.IPNet, linkIndex, tableID int) error
	GetDefaultGatewayInterface(family k8snet.IPFamily) (NetworkInterface, error)
}

const (
	allZeroAddress   = "0.0.0.0/0"
	allZeroAddressV6 = "::/0"
)

var (
	logger = log.Logger{Logger: logf.Log.WithName("netlink")}

	NewFunc func() Interface

	allZeroesFamilyAddress = map[k8snet.IPFamily]string{
		k8snet.IPv4: allZeroAddress,
		k8snet.IPv6: allZeroAddressV6,
	}
)

type netlinkType struct{}

func New() Interface {
	if NewFunc != nil {
		return NewFunc()
	}

	return &Adapter{Basic: &netlinkType{}}
}

func (n *netlinkType) LinkAdd(link netlink.Link) error {
	return netlink.LinkAdd(link)
}

func (n *netlinkType) LinkDel(link netlink.Link) error {
	return netlink.LinkDel(link)
}

func (n *netlinkType) LinkByName(name string) (netlink.Link, error) {
	return netlink.LinkByName(name)
}

func (n *netlinkType) LinkSetUp(link netlink.Link) error {
	return netlink.LinkSetUp(link)
}

func (n *netlinkType) AddrAdd(link netlink.Link, addr *netlink.Addr) error {
	return netlink.AddrAdd(link, addr)
}

func (n *netlinkType) AddrDel(link netlink.Link, addr *netlink.Addr) error {
	return netlink.AddrDel(link, addr)
}

func (n *netlinkType) AddrList(link netlink.Link, family k8snet.IPFamily) ([]netlink.Addr, error) {
	return netlink.AddrList(link, ToNetlinkFamily(family))
}

func (n *netlinkType) AddrSubscribe(addrCh chan netlink.AddrUpdate, doneCh chan struct{}) error {
	return netlink.AddrSubscribe(addrCh, doneCh)
}

func (n *netlinkType) NeighAppend(neigh *netlink.Neigh) error {
	return netlink.NeighAppend(neigh)
}

func (n *netlinkType) NeighDel(neigh *netlink.Neigh) error {
	return netlink.NeighDel(neigh)
}

func (n *netlinkType) RouteAdd(route *netlink.Route) error {
	return netlink.RouteAdd(route)
}

func (n *netlinkType) RouteDel(route *netlink.Route) error {
	return netlink.RouteDel(route)
}

func (n *netlinkType) RouteReplace(route *netlink.Route) error {
	return netlink.RouteReplace(route)
}

func (n *netlinkType) RouteGet(destination net.IP) ([]netlink.Route, error) {
	return netlink.RouteGet(destination)
}

func (n *netlinkType) RouteList(link netlink.Link, family k8snet.IPFamily) ([]netlink.Route, error) {
	return netlink.RouteList(link, ToNetlinkFamily(family))
}

func (n *netlinkType) RuleAdd(rule *netlink.Rule) error {
	return netlink.RuleAdd(rule)
}

func (n *netlinkType) RuleDel(rule *netlink.Rule) error {
	return netlink.RuleDel(rule)
}

func (n *netlinkType) RuleList(family k8snet.IPFamily) ([]netlink.Rule, error) {
	return netlink.RuleList(ToNetlinkFamily(family))
}

func (n *netlinkType) XfrmPolicyAdd(policy *netlink.XfrmPolicy) error {
	return netlink.XfrmPolicyAdd(policy)
}

func (n *netlinkType) XfrmPolicyDel(policy *netlink.XfrmPolicy) error {
	return netlink.XfrmPolicyDel(policy)
}

func (n *netlinkType) XfrmPolicyList(family k8snet.IPFamily) ([]netlink.XfrmPolicy, error) {
	return netlink.XfrmPolicyList(ToNetlinkFamily(family))
}

func (n *netlinkType) EnableLooseModeReversePathFilter(interfaceName string, family k8snet.IPFamily) error {
	if family == k8snet.IPv6 {
		return nil
	}

	// Enable loose mode (rp_filter=2) reverse path filtering on the vxlan interface.
	err := setSysctl(ipConfPath(interfaceName, family)+"/rp_filter", []byte("2"))

	return errors.Wrapf(err, "unable to update rp_filter proc entry for interface %q", interfaceName)
}

func (n *netlinkType) EnsureLooseModeIsConfigured(interfaceName string, family k8snet.IPFamily) error {
	// Enable loose mode (setting rp_filter) is supported only for IPv4
	if family == k8snet.IPv6 {
		return nil
	}

	for range 10 {
		// Revisit: This is a temporary work-around to fix https://github.com/submariner-io/submariner/issues/2422
		// Allow the interface to get initialized.
		time.Sleep(100 * time.Millisecond)

		rpFilterSetting, err := n.getReversePathFilter(interfaceName)
		if err == nil {
			if bytes.Equal(rpFilterSetting, []byte("2")) {
				return nil
			}
		} else {
			logger.Warningf("Error retrieving reverse path filter for %q: %v", interfaceName, err)
		}

		err = n.EnableLooseModeReversePathFilter(interfaceName, family)
		if err != nil {
			return errors.Wrapf(err, "error enabling loose mode on iface %q", interfaceName)
		}
	}

	return fmt.Errorf("loose mode not configured on iface %q", interfaceName)
}

func (n *netlinkType) EnableForwarding(interfaceName string, family k8snet.IPFamily) error {
	err := setSysctl(ipConfPath(interfaceName, family)+"/forwarding", []byte("1"))
	return errors.Wrapf(err, "unable to update forwarding on interface %q", interfaceName)
}

func (n *netlinkType) getReversePathFilter(interfaceName string) ([]byte, error) {
	path := ipConfPath(interfaceName, k8snet.IPv4) + "/rp_filter"

	existing, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to read proc entry for interface %q", interfaceName)
	}

	// Ignore leading and terminating newlines
	existing = bytes.Trim(existing, "\n")

	return existing, nil
}

func (n *netlinkType) FlushRouteTable(tableID int) error {
	// The conversion doesn't introduce a security problem
	// #nosec G204
	return exec.Command("/sbin/ip", "r", "flush", "table", strconv.Itoa(tableID)).Run()
}

func (n *netlinkType) ConfigureTCPMTUProbe(mtuProbe, baseMss string) error {
	err := setSysctl("/proc/sys/net/ipv4/tcp_mtu_probing", []byte(mtuProbe))
	if err != nil {
		return errors.Wrapf(err, "unable to update value of tcp_mtu_probing to %s", mtuProbe)
	}

	err = setSysctl("/proc/sys/net/ipv4/tcp_base_mss", []byte(baseMss))

	return errors.Wrapf(err, "unable to update value of tcp_base_mss to %ss", baseMss)
}

func (n *netlinkType) InterfaceByName(name string) (NetworkInterface, error) {
	i, err := net.InterfaceByName(name)
	if err != nil {
		return nil, err
	}

	return &DefaultNetworkInterface{Interface: *i}, nil
}

func (n *netlinkType) InterfaceByIndex(index int) (NetworkInterface, error) {
	i, err := net.InterfaceByIndex(index)
	if err != nil {
		return nil, err
	}

	return &DefaultNetworkInterface{Interface: *i}, nil
}

func setSysctl(path string, contents []byte) error {
	existing, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Ignore leading and terminating newlines
	existing = bytes.Trim(existing, "\n")

	if bytes.Equal(existing, contents) {
		return nil
	}
	// Permissions are already 644, the files are never created
	// #nosec G306
	return os.WriteFile(path, contents, 0o644)
}

func ipConfPath(interfaceName string, family k8snet.IPFamily) string {
	return "/proc/sys/net/ipv" + string(family) + "/conf/" + interfaceName
}

func DeleteIfaceAndAssociatedRoutes(iface string, tableID int) error {
	n := New()

	link, err := n.LinkByName(iface)
	if err != nil {
		//nolint:errorlint // netlink.LinkNotFoundError does not implement method Is(error) bool
		if _, ok := err.(netlink.LinkNotFoundError); !ok {
			logger.Warningf("Failed to retrieve the vxlan-tunnel interface: %v", err)
		}

		return nil
	}

	currentRouteList, err := n.RouteList(link, k8snet.IPv4)

	if err != nil {
		logger.Warningf("Unable to cleanup routes, error retrieving routes on the link %s: %v", iface, err)
	} else {
		for i := range currentRouteList {
			logger.V(log.DEBUG).Infof("Processing route %v", currentRouteList[i])

			if currentRouteList[i].Table == tableID {
				if err = n.RouteDel(&currentRouteList[i]); err != nil {
					logger.Errorf(err, "Error removing route %s", currentRouteList[i])
				}
			}
		}
	}

	err = n.LinkDel(link)
	if err != nil {
		return errors.Wrapf(err, "failed to delete the vxlan interface")
	}

	return nil
}

func DeleteXfrmRules(family k8snet.IPFamily) error {
	n := New()

	currentXfrmPolicyList, err := n.XfrmPolicyList(family)
	if err != nil {
		return errors.Wrap(err, "error retrieving current xfrm policies")
	}

	if len(currentXfrmPolicyList) > 0 {
		logger.Infof("Cleaning up %d XFRM policies", len(currentXfrmPolicyList))
	}

	for i := range currentXfrmPolicyList {
		// These xfrm rules are not programmed by Submariner, skip them.
		if currentXfrmPolicyList[i].Dst.String() == allZeroesFamilyAddress[family] &&
			currentXfrmPolicyList[i].Src.String() == allZeroesFamilyAddress[family] && currentXfrmPolicyList[i].Proto == 0 {
			logger.V(log.DEBUG).Infof("Skipping deletion of XFRM policy %s", currentXfrmPolicyList[i])
			continue
		}

		logger.V(log.DEBUG).Infof("Deleting XFRM policy %s", currentXfrmPolicyList[i])

		if err = n.XfrmPolicyDel(&currentXfrmPolicyList[i]); err != nil {
			return errors.Wrapf(err, "error deleting XFRM policy %s", currentXfrmPolicyList[i])
		}
	}

	return nil
}

func NewTableRule(tableID int, family k8snet.IPFamily) *netlink.Rule {
	rule := netlink.NewRule()
	rule.Table = tableID
	rule.Priority = tableID
	rule.Family = ToNetlinkFamily(family)

	return rule
}

func ToNetlinkFamily(family k8snet.IPFamily) int {
	switch family {
	case k8snet.IPv4:
		return netlink.FAMILY_V4
	case k8snet.IPv6:
		return netlink.FAMILY_V6
	case k8snet.IPFamilyUnknown:
		return netlink.FAMILY_ALL
	}

	return netlink.FAMILY_ALL
}
