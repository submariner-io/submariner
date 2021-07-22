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
	"github.com/vishvananda/netlink"
)

type NetLink struct {
	sync.Mutex
	linkIndices map[string]int
	links       map[string]netlink.Link
	routes      map[int][]netlink.Route
	neighbors   map[int][]netlink.Neigh
	rules       map[int]netlink.Rule
}

func New() *NetLink {
	return &NetLink{
		linkIndices: map[string]int{},
		links:       map[string]netlink.Link{},
		routes:      map[int][]netlink.Route{},
		neighbors:   map[int][]netlink.Neigh{},
		rules:       map[int]netlink.Rule{},
	}
}

func (n *NetLink) SetLinkIndex(name string, index int) {
	n.Lock()
	defer n.Unlock()

	n.linkIndices[name] = index
}

func (n *NetLink) LinkAdd(link netlink.Link) error {
	n.Lock()
	defer n.Unlock()

	if _, found := n.links[link.Attrs().Name]; found {
		return syscall.EEXIST
	}

	link.Attrs().Index = n.linkIndices[link.Attrs().Name]
	n.links[link.Attrs().Name] = link

	return nil
}

func (n *NetLink) LinkDel(link netlink.Link) error {
	n.Lock()
	defer n.Unlock()

	delete(n.links, link.Attrs().Name)

	return nil
}

func (n *NetLink) LinkByName(name string) (netlink.Link, error) {
	n.Lock()
	defer n.Unlock()

	link, found := n.links[name]
	if !found {
		return nil, &netlink.LinkNotFoundError{}
	}

	return link, nil
}

func (n *NetLink) LinkSetUp(link netlink.Link) error {
	return nil
}

func (n *NetLink) AddrAdd(link netlink.Link, addr *netlink.Addr) error {
	return nil
}

func (n *NetLink) NeighAppend(neigh *netlink.Neigh) error {
	n.Lock()
	defer n.Unlock()

	n.neighbors[neigh.LinkIndex] = append(n.neighbors[neigh.LinkIndex], *neigh)

	return nil
}

func (n *NetLink) NeighDel(neigh *netlink.Neigh) error {
	n.Lock()
	defer n.Unlock()

	neighbors := n.neighbors[neigh.LinkIndex]
	for i := range neighbors {
		if reflect.DeepEqual(neighbors[i], *neigh) {
			n.neighbors[neigh.LinkIndex] = append(neighbors[:i], neighbors[i+1:]...)
			break
		}
	}

	return nil
}

func (n *NetLink) RouteAdd(route *netlink.Route) error {
	n.Lock()
	defer n.Unlock()

	n.routes[route.LinkIndex] = append(n.routes[route.LinkIndex], *route)

	return nil
}

func (n *NetLink) RouteDel(route *netlink.Route) error {
	n.Lock()
	defer n.Unlock()

	routes := n.routes[route.LinkIndex]

	for i := range routes {
		if reflect.DeepEqual(routes[i], *route) {
			n.routes[route.LinkIndex] = append(routes[:i], routes[i+1:]...)
			break
		}
	}

	return nil
}

func (n *NetLink) FlushRouteTable(table int) error {
	n.Lock()
	defer n.Unlock()

	for index, routes := range n.routes {
		newRoutes := []netlink.Route{}

		for _, r := range routes {
			if r.Table != table {
				newRoutes = append(newRoutes, r)
			}
		}

		n.routes[index] = newRoutes
	}

	return nil
}

func (n *NetLink) RouteGet(destination net.IP) ([]netlink.Route, error) {
	n.Lock()
	defer n.Unlock()

	routes := []netlink.Route{}
	for _, rts := range n.routes {
		for _, r := range rts {
			if r.Dst != nil && reflect.DeepEqual(r.Dst.IP, destination) {
				routes = append(routes, r)
			}
		}
	}

	return routes, nil
}

func (n *NetLink) RouteList(link netlink.Link, family int) ([]netlink.Route, error) {
	n.Lock()
	defer n.Unlock()

	return n.routes[link.Attrs().Index], nil
}

func (n *NetLink) RuleAdd(rule *netlink.Rule) error {
	n.Lock()
	defer n.Unlock()

	if _, found := n.rules[rule.Table]; found {
		return os.ErrExist
	}

	n.rules[rule.Table] = *rule

	return nil
}

func (n *NetLink) RuleDel(rule *netlink.Rule) error {
	n.Lock()
	defer n.Unlock()

	if _, found := n.rules[rule.Table]; !found {
		return os.ErrNotExist
	}

	delete(n.rules, rule.Table)

	return nil
}

func (n *NetLink) XfrmPolicyAdd(policy *netlink.XfrmPolicy) error {
	return nil
}

func (n *NetLink) XfrmPolicyDel(policy *netlink.XfrmPolicy) error {
	return nil
}

func (n *NetLink) XfrmPolicyList(family int) ([]netlink.XfrmPolicy, error) {
	return []netlink.XfrmPolicy{}, nil
}

func (n *NetLink) EnableLooseModeReversePathFilter(interfaceName string) error {
	return nil
}

func (n *NetLink) ConfigureTCPMTUProbe(mtuProbe, baseMss string) error {
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
	Eventually(func() error {
		_, err := n.LinkByName(name)
		return err
	}, 5).Should(BeAssignableToTypeOf(&netlink.LinkNotFoundError{}), "Link %q exists", name)
}

func (n *NetLink) routeDestList(linkIndex int) []net.IPNet {
	dests := []net.IPNet{}
	routes, _ := n.RouteList(&netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Index: linkIndex,
		},
	}, 0)

	for _, r := range routes {
		if r.Dst != nil {
			dests = append(dests, *r.Dst)
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
	n.Lock()
	defer n.Unlock()

	ips := []net.IP{}
	for _, neigh := range n.neighbors[linkIndex] {
		ips = append(ips, neigh.IP)
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
	n.Lock()
	defer n.Unlock()

	r, found := n.rules[table]
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
