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
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/vishvananda/netlink"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type Basic interface {
	LinkAdd(link netlink.Link) error
	LinkDel(link netlink.Link) error
	LinkByName(name string) (netlink.Link, error)
	LinkSetUp(link netlink.Link) error
	AddrAdd(link netlink.Link, addr *netlink.Addr) error
	NeighAppend(neigh *netlink.Neigh) error
	NeighDel(neigh *netlink.Neigh) error
	RouteAdd(route *netlink.Route) error
	RouteDel(route *netlink.Route) error
	RouteGet(destination net.IP) ([]netlink.Route, error)
	RouteList(link netlink.Link, family int) ([]netlink.Route, error)
	FlushRouteTable(tableID int) error
	RuleAdd(rule *netlink.Rule) error
	RuleDel(rule *netlink.Rule) error
	XfrmPolicyAdd(policy *netlink.XfrmPolicy) error
	XfrmPolicyDel(policy *netlink.XfrmPolicy) error
	XfrmPolicyList(family int) ([]netlink.XfrmPolicy, error)
	EnableLooseModeReversePathFilter(interfaceName string) error
	EnsureLooseModeIsConfigured(interfaceName string) error
	EnableForwarding(interfaceName string) error
	GetReversePathFilter(interfaceName string) ([]byte, error)
	ConfigureTCPMTUProbe(mtuProbe, baseMss string) error
}

type Interface interface {
	Basic
	AddrAddIfNotPresent(link netlink.Link, addr *netlink.Addr) error
	RuleAddIfNotPresent(rule *netlink.Rule) error
	RuleDelIfPresent(rule *netlink.Rule) error
	RouteAddOrReplace(route *netlink.Route) error
	AddDestinationRoutes(destIPs []net.IPNet, gwIP, srcIP net.IP, linkIndex, tableID int) error
	DeleteDestinationRoutes(destIPs []net.IPNet, linkIndex, tableID int) error
}

var logger = log.Logger{Logger: logf.Log.WithName("netlink")}

var NewFunc func() Interface

const (
	allZeroAddress = "0.0.0.0/0"
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

func (n *netlinkType) RouteGet(destination net.IP) ([]netlink.Route, error) {
	return netlink.RouteGet(destination)
}

func (n *netlinkType) RouteList(link netlink.Link, family int) ([]netlink.Route, error) {
	return netlink.RouteList(link, family)
}

func (n *netlinkType) RuleAdd(rule *netlink.Rule) error {
	return netlink.RuleAdd(rule)
}

func (n *netlinkType) RuleDel(rule *netlink.Rule) error {
	return netlink.RuleDel(rule)
}

func (n *netlinkType) XfrmPolicyAdd(policy *netlink.XfrmPolicy) error {
	return netlink.XfrmPolicyAdd(policy)
}

func (n *netlinkType) XfrmPolicyDel(policy *netlink.XfrmPolicy) error {
	return netlink.XfrmPolicyDel(policy)
}

func (n *netlinkType) XfrmPolicyList(family int) ([]netlink.XfrmPolicy, error) {
	return netlink.XfrmPolicyList(family)
}

func (n *netlinkType) EnableLooseModeReversePathFilter(interfaceName string) error {
	// Enable loose mode (rp_filter=2) reverse path filtering on the vxlan interface.
	err := setSysctl(ipv4ConfPath(interfaceName)+"/rp_filter", []byte("2"))
	return errors.Wrapf(err, "unable to update rp_filter proc entry for interface %q", interfaceName)
}

func (n *netlinkType) EnsureLooseModeIsConfigured(interfaceName string) error {
	for i := 0; i < 10; i++ {
		// Revisit: This is a temporary work-around to fix https://github.com/submariner-io/submariner/issues/2422
		// Allow the interface to get initialized.
		time.Sleep(100 * time.Millisecond)

		rpFilterSetting, err := n.GetReversePathFilter(interfaceName)
		if err == nil {
			if bytes.Equal(rpFilterSetting, []byte("2")) {
				return nil
			}
		} else {
			logger.Warningf("Error retrieving reverse path filter for %q: %v", interfaceName, err)
		}

		err = n.EnableLooseModeReversePathFilter(interfaceName)
		if err != nil {
			return errors.Wrapf(err, "error enabling loose mode on iface %q", interfaceName)
		}
	}

	return fmt.Errorf("loose mode not configured on iface %q", interfaceName)
}

func (n *netlinkType) EnableForwarding(interfaceName string) error {
	err := setSysctl(ipv4ConfPath(interfaceName)+"/forwarding", []byte("1"))
	return errors.Wrapf(err, "unable to update forwarding on interface %q", interfaceName)
}

func (n *netlinkType) GetReversePathFilter(interfaceName string) ([]byte, error) {
	path := ipv4ConfPath(interfaceName) + "/rp_filter"

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

func ipv4ConfPath(interfaceName string) string {
	return "/proc/sys/net/ipv4/conf/" + interfaceName
}

//nolint:wrapcheck // Let the caller wrap external errors
func GetDefaultGatewayInterface() (*net.Interface, error) {
	routes, err := netlink.RouteList(nil, syscall.AF_INET)
	if err != nil {
		return nil, err
	}

	for i := range routes {
		if routes[i].Dst == nil || routes[i].Dst.String() == allZeroAddress {
			if routes[i].LinkIndex == 0 {
				return nil, fmt.Errorf("default gateway interface could not be determined")
			}

			iface, err := net.InterfaceByIndex(routes[i].LinkIndex)
			if err != nil {
				return nil, err
			}

			return iface, nil
		}
	}

	return nil, fmt.Errorf("unable to find default route")
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

	currentRouteList, err := n.RouteList(link, syscall.AF_INET)

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

func DeleteXfrmRules() error {
	n := New()

	currentXfrmPolicyList, err := n.XfrmPolicyList(syscall.AF_INET)
	if err != nil {
		return errors.Wrap(err, "error retrieving current xfrm policies")
	}

	if len(currentXfrmPolicyList) > 0 {
		logger.Infof("Cleaning up %d XFRM policies", len(currentXfrmPolicyList))
	}

	for i := range currentXfrmPolicyList {
		// These xfrm rules are not programmed by Submariner, skip them.
		if currentXfrmPolicyList[i].Dst.String() == allZeroAddress &&
			currentXfrmPolicyList[i].Src.String() == allZeroAddress && currentXfrmPolicyList[i].Proto == 0 {
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

func NewTableRule(tableID int) *netlink.Rule {
	rule := netlink.NewRule()
	rule.Table = tableID
	rule.Priority = tableID

	return rule
}
