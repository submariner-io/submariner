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

package kubeproxy

import (
	"net"
	"os"
	"syscall"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/stringset"
	"github.com/submariner-io/submariner/pkg/cable/wireguard"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"k8s.io/klog"
)

func (kp *SyncHandler) updateRoutingRulesForHostNetworkSupport(inputCidrBlocks []string, operation Operation) {
	if operation == Flush {
		kp.routeCacheGWNode.RemoveAll()

		err := kp.netLink.FlushRouteTable(constants.RouteAgentHostNetworkTableID)
		if err != nil {
			// We can safely ignore this error, as this table will exist only on GW nodes
			klog.V(log.TRACE).Infof("Flushing routing table %d returned error. Can be ignored on non-Gw node: %v",
				constants.RouteAgentHostNetworkTableID, err)
		}

		return
	}

	if !kp.isGatewayNode || kp.cniIface == nil {
		return
	}

	// These routing rules are required ONLY on the Gateway Node.
	// On the non-Gateway nodes, we use iptable rules to support this use-case.
	for _, inputCidrBlock := range inputCidrBlocks {
		kp.updateRoutingRulesForCIDRBlock(inputCidrBlock, operation)
	}
}

func (kp *SyncHandler) updateRoutingRulesForCIDRBlock(inputCidrBlock string, operation Operation) {
	var viaGW *net.IP

	if kp.isGatewayInRemoteCIDR(inputCidrBlock) {
		gwIP := kp.remoteSubnetGw[inputCidrBlock]

		routes, err := kp.netLink.RouteGet(gwIP)
		if err != nil {
			klog.Errorf("Failed to find route to remote gateway IP %s for cidr %s", gwIP.String(), inputCidrBlock)
		}

		viaGW = &routes[0].Gw
	}

	switch operation {
	case Add:
		if kp.routeCacheGWNode.Add(inputCidrBlock) {
			if err := kp.configureRoute(inputCidrBlock, operation, viaGW); err != nil {
				kp.routeCacheGWNode.Remove(inputCidrBlock)
				klog.Errorf("Failed to add route %q for HostNetwork support on the Gateway node: %v",
					inputCidrBlock, err)
			}
		}

	case Delete:
		if kp.routeCacheGWNode.Remove(inputCidrBlock) {
			if err := kp.configureRoute(inputCidrBlock, operation, viaGW); err != nil {
				klog.Errorf("Failed to delete route %q for HostNetwork support on the Gateway node. %v",
					inputCidrBlock, err)
			}
		}
	case Flush:
	}
}

func (kp *SyncHandler) isGatewayInRemoteCIDR(remoteCIDR string) bool {
	gwIP, ok := kp.remoteSubnetGw[remoteCIDR]
	if ok {
		_, ipnet, _ := net.ParseCIDR(remoteCIDR)
		return ipnet.Contains(gwIP)
	}

	return false
}

func (kp *SyncHandler) configureRoute(remoteSubnet string, operation Operation, viaGw *net.IP) error {
	src := net.ParseIP(kp.cniIface.IPAddress)

	_, dst, err := net.ParseCIDR(remoteSubnet)
	if err != nil {
		return errors.Wrapf(err, "error parsing cidr block %s", remoteSubnet)
	}

	ifaceIndex := kp.defaultHostIface.Index
	// TODO: Add support for this in the CableDrivers themselves.
	if kp.localCableDriver == "wireguard" {
		if wg, err := net.InterfaceByName(wireguard.DefaultDeviceName); err == nil {
			ifaceIndex = wg.Index
		} else {
			klog.Errorf("Wireguard interface %s not found on the node.", wireguard.DefaultDeviceName)
		}
	}

	route := netlink.Route{
		Dst:       dst,
		Src:       src,
		LinkIndex: ifaceIndex,
		Protocol:  4,
		Table:     constants.RouteAgentHostNetworkTableID,
	}

	// in some cases we need to specify the next hop (for example when the remote ipsec endpoint
	// belongs in the remote cluster CIDR of the rule) ( see issue #1106 )
	if viaGw != nil {
		route.Gw = *viaGw
	} else {
		route.Scope = unix.RT_SCOPE_LINK
	}

	switch operation {
	case Add:
		err = kp.netLink.RouteAdd(&route)
		if err != nil && !os.IsExist(err) {
			return errors.Wrapf(err, "error adding the route %s", route)
		}
	case Delete:
		err = kp.netLink.RouteDel(&route)
		if err != nil {
			return errors.Wrapf(err, "error deleting the route %s", route)
		}
	case Flush:
	}

	return nil
}

type SubRouteKey struct {
	dst string
	gws stringset.Interface
}

func (kp *SyncHandler) listExistingRoutes() (map[SubRouteKey]netlink.Route, error) {
	currentRoutes := map[SubRouteKey]netlink.Route{}

	currentRouteList, err := kp.netLink.RouteList(kp.vxlanDevice.link, syscall.AF_INET)
	if err != nil {
		return nil, errors.Wrapf(err, "error retrieving routes for link %s", VxLANIface)
	}

	for _, route := range currentRouteList {
		gws := stringset.NewSynchronized()
		for _, nextHop := range route.MultiPath {
			gws.Add(nextHop.Gw.String())
		}
		key := SubRouteKey{dst: route.Dst.String(), gws: gws}
		currentRoutes[key] = route
	}

	return currentRoutes, nil
}

// buildDesiredRoutes will make all routes needed for intra cluster communication within
// submariner. Specifically it will route traffic originating on a worker node, destined
// for another cluster, to an active GW
func (kp *SyncHandler) buildDesiredRoutes() (map[SubRouteKey]netlink.Route, error) {
	desiredRoutes := map[SubRouteKey]netlink.Route{}

	nextHops := []*netlink.NexthopInfo{}

	for _, gw := range kp.gwVTEPs.Elements() {
		nextHops = append(nextHops, &netlink.NexthopInfo{
			Gw:        net.ParseIP(gw),
			LinkIndex: kp.vxlanDevice.link.Attrs().Index,
		})
	}

	for _, remoteSubnet := range kp.remoteSubnets.Elements() {
		key := SubRouteKey{dst: remoteSubnet, gws: kp.gwVTEPs}

		_, dst, err := net.ParseCIDR(remoteSubnet)
		if err != nil {
			return nil, errors.Errorf("Error parsing cidr block %s: %v", remoteSubnet, err)
		}

		route := netlink.Route{
			Dst:       dst,
			MultiPath: nextHops,
			Scope:     unix.RT_SCOPE_UNIVERSE,
			LinkIndex: kp.vxlanDevice.link.Attrs().Index,
			Protocol:  4,
		}

		desiredRoutes[key] = route
	}

	return desiredRoutes, nil
}

// Reconcile the routes based on the GWIPs installed on this device using rtnetlink.
func (kp *SyncHandler) reconcileIntraClusterRoutes() error {
	currentRouteList, err := kp.listExistingRoutes()
	if err != nil {
		return errors.Wrapf(err, "error retrieving routes for link %s", VxLANIface)
	}

	if kp.isGatewayNode {
		klog.V(log.DEBUG).Infof("Node is a GW, delete all intra cluster Routes on %v", VxLANIface)
		for _, route := range currentRouteList {
			klog.V(log.DEBUG).Infof("Node is a GW Removing route %s", route.String())
			if err = kp.netLink.RouteDel(&route); err != nil {
				klog.Errorf("Error removing route %s: %v", route, err)
			}
		}
		return nil
	}

	klog.V(log.DEBUG).Infof("Reconciling existing routes %v to gws %v on worker node", currentRouteList, kp.gwVTEPs.Elements())

	desiredRouteList, err := kp.buildDesiredRoutes()
	if err != nil {
		return errors.Wrapf(err, "error retrieving desired routes for link %s", VxLANIface)
	}

	for key, route := range currentRouteList {
		// route exists and is up to date keep it
		if _, ok := desiredRouteList[key]; ok {
			delete(desiredRouteList, key)
			continue
		}

		klog.V(log.DEBUG).Infof("Current Route %v will I delete?for %+v %v", route, kp.vxlanDevice.link, route.Src.Equal(kp.vxlanDevice.link.SrcAddr))
		// don't remove auto-generated route for vxlan-submariner
		if kp.vxlanDevice != nil && route.Src.Equal(kp.vxlanDevice.link.SrcAddr) {
			klog.V(log.DEBUG).Infof("Skipping removal of autogenerated route: %s", route)
			continue
		}

		// if not in list delete it
		klog.V(log.DEBUG).Infof("Removing stale route %s", route)
		if err = kp.netLink.RouteDel(&route); err != nil {
			klog.Errorf("Error removing route %s: %v", route, err)
		}
	}

	// Let's now add the routes that are missing.
	for _, route := range desiredRouteList {
		// if in desiredRoute list add it.
		klog.V(log.DEBUG).Infof("Adding new route %s", route)
		err = kp.netLink.RouteAdd(&route)
		if err != nil {
			klog.Errorf("Error adding route %s: %v", route, err)
		}
	}

	return nil
}

func (kp *SyncHandler) configureIPRule(operation Operation) error {
	if kp.cniIface != nil {
		rule := netlink.NewRule()
		rule.Table = constants.RouteAgentHostNetworkTableID
		rule.Priority = constants.RouteAgentHostNetworkTableID

		switch operation {
		case Add:
			err := kp.netLink.RuleAdd(rule)
			if err != nil && !os.IsExist(err) {
				return errors.Wrapf(err, "failed to add ip rule %s", rule)
			}
		case Delete:
			err := kp.netLink.RuleDel(rule)
			if err != nil && !os.IsNotExist(err) {
				return errors.Wrapf(err, "failed to delete ip rule %s", rule)
			}
		case Flush:
		}
	}

	return nil
}
