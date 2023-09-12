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

package fake

import (
	"net"
	"os"
	"reflect"
	"sync"
	"syscall"

	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	"github.com/vishvananda/netlink"
)

type basicType struct {
	mutex       sync.Mutex
	linkIndices map[string]int
	links       map[string]netlink.Link
	routes      map[int][]netlink.Route
	neighbors   map[int][]netlink.Neigh
	rules       map[int]netlink.Rule
}

type NetLink struct {
	netlinkAPI.Adapter
}

var _ netlinkAPI.Interface = &NetLink{}

type linkNotFoundError struct{}

func (e linkNotFoundError) Error() string {
	return "Link not found"
}

func (e linkNotFoundError) Is(err error) bool {
	_, ok := err.(netlink.LinkNotFoundError)
	return ok
}

func New() *NetLink {
	return &NetLink{
		Adapter: netlinkAPI.Adapter{Basic: &basicType{
			linkIndices: map[string]int{},
			links:       map[string]netlink.Link{},
			routes:      map[int][]netlink.Route{},
			neighbors:   map[int][]netlink.Neigh{},
			rules:       map[int]netlink.Rule{},
		}},
	}
}

func (n *NetLink) basic() *basicType {
	return n.Adapter.Basic.(*basicType)
}

func (n *NetLink) SetLinkIndex(name string, index int) {
	n.basic().mutex.Lock()
	defer n.basic().mutex.Unlock()

	n.basic().linkIndices[name] = index
}

func (n *basicType) LinkAdd(link netlink.Link) error {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	if _, found := n.links[link.Attrs().Name]; found {
		return syscall.EEXIST
	}

	link.Attrs().Index = n.linkIndices[link.Attrs().Name]
	n.links[link.Attrs().Name] = link

	return nil
}

func (n *basicType) LinkDel(link netlink.Link) error {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	delete(n.links, link.Attrs().Name)

	return nil
}

func (n *basicType) LinkByName(name string) (netlink.Link, error) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	link, found := n.links[name]
	if !found {
		return nil, linkNotFoundError{}
	}

	return link, nil
}

func (n *basicType) LinkSetUp(_ netlink.Link) error {
	return nil
}

func (n *basicType) AddrAdd(_ netlink.Link, _ *netlink.Addr) error {
	return nil
}

func (n *basicType) NeighAppend(neigh *netlink.Neigh) error {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	n.neighbors[neigh.LinkIndex] = append(n.neighbors[neigh.LinkIndex], *neigh)

	return nil
}

func (n *basicType) NeighDel(neigh *netlink.Neigh) error {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	neighbors := n.neighbors[neigh.LinkIndex]
	for i := range neighbors {
		if reflect.DeepEqual(neighbors[i], *neigh) {
			n.neighbors[neigh.LinkIndex] = append(neighbors[:i], neighbors[i+1:]...)
			break
		}
	}

	return nil
}

func (n *basicType) RouteAdd(route *netlink.Route) error {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	n.routes[route.LinkIndex] = append(n.routes[route.LinkIndex], *route)

	return nil
}

func (n *basicType) RouteDel(route *netlink.Route) error {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	routes := n.routes[route.LinkIndex]

	for i := range routes {
		if reflect.DeepEqual(routes[i], *route) {
			n.routes[route.LinkIndex] = append(routes[:i], routes[i+1:]...)
			break
		}
	}

	return nil
}

func (n *basicType) FlushRouteTable(table int) error {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	for index, routes := range n.routes {
		newRoutes := []netlink.Route{}

		for i := range routes {
			if routes[i].Table != table {
				newRoutes = append(newRoutes, routes[i])
			}
		}

		n.routes[index] = newRoutes
	}

	return nil
}

func (n *basicType) RouteGet(destination net.IP) ([]netlink.Route, error) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	routes := []netlink.Route{}
	for i := range n.routes {
		for j := range n.routes[i] {
			if n.routes[i][j].Dst != nil && reflect.DeepEqual(n.routes[i][j].Dst.IP, destination) {
				routes = append(routes, n.routes[i][j])
			}
		}
	}

	return routes, nil
}

func (n *basicType) RouteList(link netlink.Link, _ int) ([]netlink.Route, error) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	return n.routes[link.Attrs().Index], nil
}

func (n *basicType) RuleAdd(rule *netlink.Rule) error {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	if _, found := n.rules[rule.Table]; found {
		return os.ErrExist
	}

	n.rules[rule.Table] = *rule

	return nil
}

func (n *basicType) RuleDel(rule *netlink.Rule) error {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	if _, found := n.rules[rule.Table]; !found {
		return os.ErrNotExist
	}

	delete(n.rules, rule.Table)

	return nil
}

func (n *basicType) XfrmPolicyAdd(_ *netlink.XfrmPolicy) error {
	return nil
}

func (n *basicType) XfrmPolicyDel(_ *netlink.XfrmPolicy) error {
	return nil
}

func (n *basicType) XfrmPolicyList(_ int) ([]netlink.XfrmPolicy, error) {
	return []netlink.XfrmPolicy{}, nil
}

func (n *basicType) EnableLooseModeReversePathFilter(_ string) error {
	return nil
}

func (n *basicType) EnsureLooseModeIsConfigured(_ string) error {
	return nil
}

func (n *basicType) EnableForwarding(_ string) error {
	return nil
}

func (n *basicType) GetReversePathFilter(_ string) ([]byte, error) {
	return []byte("2"), nil
}

func (n *basicType) ConfigureTCPMTUProbe(_, _ string) error {
	return nil
}

func (n *NetLink) AwaitLink(name string) (link netlink.Link) {
	Eventually(func() netlink.Link {
		link, _ = n.LinkByName(name)
		return link
	}, 5).ShouldNot(BeNil(), "Link %q not found", name)

	return
}

func (n *NetLink) AwaitNoLink(name string) {
	Eventually(func() bool {
		_, err := n.LinkByName(name)
		return errors.Is(err, netlink.LinkNotFoundError{})
	}, 5).Should(BeTrue(), "Link %q exists", name)
}

func (n *NetLink) routeDestList(linkIndex int) []net.IPNet {
	dests := []net.IPNet{}
	routes, _ := n.RouteList(&netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Index: linkIndex,
		},
	}, 0)

	for i := range routes {
		if routes[i].Dst != nil {
			dests = append(dests, *routes[i].Dst)
		}
	}

	return dests
}

func (n *NetLink) AwaitRoutes(linkIndex int, destCIDRs ...string) {
	for _, destCIDR := range destCIDRs {
		_, expDest, _ := net.ParseCIDR(destCIDR)

		Eventually(func() []net.IPNet {
			return n.routeDestList(linkIndex)
		}, 5).Should(ContainElement(*expDest), "Route for %q not found", destCIDR)
	}
}

func (n *NetLink) AwaitNoRoutes(linkIndex int, destCIDRs ...string) {
	for _, destCIDR := range destCIDRs {
		_, expDest, _ := net.ParseCIDR(destCIDR)

		Eventually(func() []net.IPNet {
			return n.routeDestList(linkIndex)
		}, 5).ShouldNot(ContainElement(*expDest), "Route for %q exists", destCIDR)
	}
}

func (n *NetLink) neighborIPList(linkIndex int) []net.IP {
	n.basic().mutex.Lock()
	defer n.basic().mutex.Unlock()

	ips := []net.IP{}
	for i := range n.basic().neighbors[linkIndex] {
		ips = append(ips, n.basic().neighbors[linkIndex][i].IP)
	}

	return ips
}

func (n *NetLink) AwaitNeighbors(linkIndex int, expIPs ...string) {
	for _, ip := range expIPs {
		expIP := net.ParseIP(ip)

		Eventually(func() []net.IP {
			return n.neighborIPList(linkIndex)
		}, 5).Should(ContainElement(expIP), "Neighbor for %q not found", expIP)
	}
}

func (n *NetLink) AwaitNoNeighbors(linkIndex int, expIPs ...string) {
	for _, ip := range expIPs {
		expIP := net.ParseIP(ip)

		Eventually(func() []net.IP {
			return n.neighborIPList(linkIndex)
		}, 5).ShouldNot(ContainElement(expIP), "Neighbor for %q exists", expIP)
	}
}

func (n *NetLink) getRule(table int) *netlink.Rule {
	n.basic().mutex.Lock()
	defer n.basic().mutex.Unlock()

	r, found := n.basic().rules[table]
	if !found {
		return nil
	}

	return &r
}

func (n *NetLink) AwaitRule(table int) {
	Eventually(func() *netlink.Rule {
		return n.getRule(table)
	}, 5).ShouldNot(BeNil(), "Rule for %v not found", table)
}

func (n *NetLink) AwaitNoRule(table int) {
	Eventually(func() *netlink.Rule {
		return n.getRule(table)
	}, 5).Should(BeNil(), "Rule for %v exists", table)
}
