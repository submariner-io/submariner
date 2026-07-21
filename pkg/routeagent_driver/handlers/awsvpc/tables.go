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

package awsvpc

import (
	"net"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	k8snet "k8s.io/utils/net"
)

// syncCNITableRoutesLocked copies remote cluster CIDRs and the VTEP overlay into
// AWS VPC CNI PBR tables (issue #3697). Must be called with h.mutex held.
func (h *Handler) syncCNITableRoutesLocked() {
	if h.State().IsOnGateway() {
		// On the active gateway, inter-cluster traffic uses the cable interface
		// directly; workers need CNI table replication.
		return
	}

	tables, err := h.discoverCNIRoutingTables()
	if err != nil {
		logger.Errorf(err, "Failed to list CNI routing tables")
		return
	}

	if len(tables) == 0 {
		return
	}

	link, err := h.netLink.LinkByName(vxlanIfaceName)
	if err != nil {
		logger.V(log.DEBUG).Infof("vx-submariner not ready yet: %v", err)
		return
	}

	gwIP := h.localGatewayVTEP()
	if gwIP == nil {
		logger.V(log.DEBUG).Info("Local gateway VTEP unknown; skip CNI table sync")
		return
	}

	cidrs := append(h.remoteCIDRs.UnsortedList(), vtepPrefixCIDR)

	for _, table := range tables {
		for _, cidrStr := range cidrs {
			if err := h.ensureTableRoute(link, gwIP, cidrStr, table); err != nil {
				logger.Errorf(err, "Failed to program %s in table %d", cidrStr, table)
			}
		}
	}
}

func (h *Handler) clearCNITableRoutesLocked() {
	tables, err := h.discoverCNIRoutingTables()
	if err != nil {
		logger.Errorf(err, "Failed to list CNI routing tables during cleanup")
		return
	}

	link, err := h.netLink.LinkByName(vxlanIfaceName)
	if err != nil {
		return
	}

	cidrs := append(h.remoteCIDRs.UnsortedList(), vtepPrefixCIDR)

	for _, table := range tables {
		for _, cidrStr := range cidrs {
			_, dst, err := net.ParseCIDR(cidrStr)
			if err != nil {
				continue
			}

			_ = h.netLink.RouteDel(&netlink.Route{
				Dst:       dst,
				LinkIndex: link.Attrs().Index,
				Table:     table,
				Protocol:  routeProtocol,
			})
		}
	}
}

func (h *Handler) ensureTableRoute(link netlink.Link, gwIP net.IP, cidrStr string, table int) error {
	_, dst, err := net.ParseCIDR(cidrStr)
	if err != nil {
		return errors.Wrapf(err, "parse %s", cidrStr)
	}

	route := &netlink.Route{
		Dst:       dst,
		LinkIndex: link.Attrs().Index,
		Scope:     unix.RT_SCOPE_UNIVERSE,
		Protocol:  routeProtocol,
		Table:     table,
	}

	// VTEP CIDR is on-link; remote CIDRs go via the local gateway VTEP.
	if cidrStr == vtepPrefixCIDR {
		route.Scope = unix.RT_SCOPE_LINK
	} else {
		route.Gw = gwIP
	}

	return errors.Wrapf(h.netLink.RouteAddOrReplace(route), "table %d route %s", table, cidrStr)
}

func (h *Handler) discoverCNIRoutingTables() ([]int, error) {
	rules, err := h.netLink.RuleList(k8snet.IPv4)
	if err != nil {
		return nil, errors.Wrap(err, "RuleList")
	}

	seen := map[int]struct{}{}
	var tables []int

	for i := range rules {
		table := rules[i].Table
		if table == 0 || table == mainRoutingTableID || table == unix.RT_TABLE_LOCAL {
			continue
		}

		// Skip Submariner-managed tables.
		if table == constants.RouteAgentInterClusterNetworkTableID ||
			table == constants.RouteAgentHostNetworkTableID ||
			table == awsVPCNodeIngressTableID {
			continue
		}

		// AWS CNI uses "from <podIP> lookup <table>".
		if rules[i].Src == nil {
			continue
		}

		if _, ok := seen[table]; ok {
			continue
		}

		seen[table] = struct{}{}
		tables = append(tables, table)
	}

	return tables, nil
}

func (h *Handler) localGatewayVTEP() net.IP {
	// Prefer the vxlan Group / neigh path: route to remote via vx-submariner uses Gw = local GW VTEP.
	link, err := h.netLink.LinkByName(vxlanIfaceName)
	if err != nil {
		return nil
	}

	routes, err := h.netLink.RouteList(link, k8snet.IPv4)
	if err != nil {
		return nil
	}

	for i := range routes {
		if routes[i].Gw != nil && !routes[i].Gw.IsUnspecified() {
			return routes[i].Gw
		}
	}

	return nil
}
