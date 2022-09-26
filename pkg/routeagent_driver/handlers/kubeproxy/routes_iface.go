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
	"github.com/submariner-io/submariner/pkg/cable/wireguard"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func (kp *SyncHandler) updateRoutingRulesForHostNetworkSupport(inputCidrBlocks []string, operation Operation) {
	if operation == Flush {
		kp.routeCacheGWNode.RemoveAll()

		err := kp.netLink.FlushRouteTable(constants.RouteAgentHostNetworkTableID)
		if err != nil {
			// We can safely ignore this error, as this table will exist only on GW nodes
			logger.V(log.TRACE).Infof("Flushing routing table %d returned error. Can be ignored on non-Gw node: %v",
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
			logger.Errorf(err, "Failed to find route to remote gateway IP %s for cidr %s", gwIP.String(), inputCidrBlock)
		}

		viaGW = &routes[0].Gw
	}

	switch operation {
	case Add:
		if kp.routeCacheGWNode.Add(inputCidrBlock) {
			if err := kp.configureRoute(inputCidrBlock, operation, viaGW); err != nil {
				kp.routeCacheGWNode.Remove(inputCidrBlock)
				logger.Errorf(err, "Failed to add route %q for HostNetwork support on the Gateway node: %v",
					inputCidrBlock, err)
			}
		}

	case Delete:
		if kp.routeCacheGWNode.Remove(inputCidrBlock) {
			if err := kp.configureRoute(inputCidrBlock, operation, viaGW); err != nil {
				logger.Errorf(err, "Failed to delete route %q for HostNetwork support on the Gateway node",
					inputCidrBlock)
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
			logger.Errorf(nil, "Wireguard interface %s not found on the node.", wireguard.DefaultDeviceName)
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

func (kp *SyncHandler) cleanVxSubmarinerRoutes() {
	link, err := kp.netLink.LinkByName(VxLANIface)
	if err != nil {
		if !errors.Is(err, netlink.LinkNotFoundError{}) {
			logger.Errorf(err, "Error retrieving link by name %q", VxLANIface)
		}

		return
	}

	currentRouteList, err := kp.netLink.RouteList(link, syscall.AF_INET)
	if err != nil {
		logger.Errorf(err, "Unable to cleanup routes, error retrieving routes on the link %s", VxLANIface)
		return
	}

	for i := range currentRouteList {
		logger.V(log.DEBUG).Infof("Processing route %v", currentRouteList[i])

		if currentRouteList[i].Dst == nil || currentRouteList[i].Gw == nil {
			logger.V(log.DEBUG).Infof("Found nil gw or dst")
		} else if kp.remoteSubnets.Contains(currentRouteList[i].Dst.String()) {
			logger.V(log.DEBUG).Infof("Removing route %s", currentRouteList[i])
			if err = kp.netLink.RouteDel(&currentRouteList[i]); err != nil {
				logger.Errorf(err, "Error removing route %s", currentRouteList[i])
			}
		}
	}
}

// Reconcile the routes installed on this device using rtnetlink.
func (kp *SyncHandler) reconcileRoutes(vxlanGw net.IP) error {
	logger.V(log.DEBUG).Infof("Reconciling routes to gw: %s", vxlanGw.String())

	link, err := kp.netLink.LinkByName(VxLANIface)
	if err != nil {
		return errors.Wrapf(err, "error retrieving link by name %s", VxLANIface)
	}

	currentRouteList, err := kp.netLink.RouteList(link, syscall.AF_INET)
	if err != nil {
		return errors.Wrapf(err, "error retrieving routes for link %s", VxLANIface)
	}

	// First lets delete all of the routes that don't match.
	kp.removeUnknownRoutes(vxlanGw, currentRouteList)

	currentRouteList, err = kp.netLink.RouteList(link, syscall.AF_INET)

	if err != nil {
		return errors.Wrapf(err, "error retrieving routes for link %s", VxLANIface)
	}

	// Let's now add the routes that are missing.
	for _, cidrBlock := range kp.remoteSubnets.Elements() {
		_, dst, err := net.ParseCIDR(cidrBlock)
		if err != nil {
			logger.Errorf(err, "Error parsing cidr block %s", cidrBlock)
			break
		}

		route := netlink.Route{
			Dst:       dst,
			Gw:        vxlanGw,
			Scope:     unix.RT_SCOPE_UNIVERSE,
			LinkIndex: link.Attrs().Index,
			Protocol:  4,
		}

		found := false

		for i := range currentRouteList {
			if currentRouteList[i].Gw == nil || currentRouteList[i].Dst == nil {
			} else if currentRouteList[i].Gw.Equal(route.Gw) && currentRouteList[i].Dst.String() == route.Dst.String() {
				logger.V(log.DEBUG).Infof("Found equivalent route, not adding")
				found = true
			}
		}

		if !found {
			err = kp.netLink.RouteAdd(&route)
			if err != nil {
				logger.Errorf(err, "Error adding route %s", route)
			}
		}
	}

	return nil
}

func (kp *SyncHandler) removeUnknownRoutes(vxlanGw net.IP, currentRouteList []netlink.Route) {
	for i := range currentRouteList {
		// Contains(endpoint destinations, route destination string, and the route gateway is our actual destination.
		logger.V(log.DEBUG).Infof("Processing route %v", currentRouteList[i])

		if currentRouteList[i].Dst == nil || currentRouteList[i].Gw == nil {
			logger.V(log.DEBUG).Infof("Found nil gw or dst")
		} else {
			if kp.remoteSubnets.Contains(currentRouteList[i].Dst.String()) && currentRouteList[i].Gw.Equal(vxlanGw) {
				logger.V(log.DEBUG).Infof("Found route %s with gw %s already installed", currentRouteList[i], currentRouteList[i].Gw)
			} else {
				logger.V(log.DEBUG).Infof("Removing route %s", currentRouteList[i])
				if err := kp.netLink.RouteDel(&currentRouteList[i]); err != nil {
					logger.Errorf(err, "Error removing route %s", currentRouteList[i])
				}
			}
		}
	}
}

func (kp *SyncHandler) updateRoutingRulesForInterClusterSupport(remoteCIDRs []string, operation Operation) error {
	if kp.isGatewayNode {
		logger.V(log.DEBUG).Info("On GWNode, in updateRoutingRulesForInterClusterSupport ignoring")
		// These rules are required only on the nonGatewayNode.
		return nil
	}

	if kp.vxlanDevice != nil && kp.vxlanGwIP != nil {
		link, err := kp.netLink.LinkByName(VxLANIface)
		if err != nil {
			return errors.Wrapf(err, "error retrieving link by name %s", VxLANIface)
		}

		for _, cidrBlock := range remoteCIDRs {
			_, dst, err := net.ParseCIDR(cidrBlock)
			if err != nil {
				return errors.Wrapf(err, "error parsing cidr block %s", cidrBlock)
			}

			route := netlink.Route{
				Dst:       dst,
				Gw:        *kp.vxlanGwIP,
				Scope:     unix.RT_SCOPE_UNIVERSE,
				LinkIndex: link.Attrs().Index,
				Protocol:  4,
			}

			if operation == Add {
				err = kp.netLink.RouteAdd(&route)
				if err != nil {
					return errors.Wrapf(err, "error adding route %s", route)
				}
			} else if operation == Delete {
				err = kp.netLink.RouteDel(&route)
				if err != nil {
					return errors.Wrapf(err, "error deleting route %s", route)
				}
			}
		}
	}

	return nil
}
