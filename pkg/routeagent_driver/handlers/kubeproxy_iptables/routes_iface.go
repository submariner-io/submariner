package kubeproxy_iptables

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"k8s.io/klog"

	"github.com/submariner-io/admiral/pkg/log"

	"github.com/submariner-io/submariner/pkg/cable/wireguard"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
)

func (kp *SyncHandler) updateRoutingRulesForHostNetworkSupport(inputCidrBlocks []string, operation Operation) {
	if operation == Flush {
		kp.routeCacheGWNode.RemoveAll()
		// The conversion doesn't introduce a security problem
		// #nosec G204
		cmd := exec.Command("/sbin/ip", "r", "flush", "table", strconv.Itoa(constants.RouteAgentHostNetworkTableID))
		if err := cmd.Run(); err != nil {
			// We can safely ignore this error, as this table will exist only on GW nodes
			klog.V(log.TRACE).Infof("Flushing routing table %d returned error. Can be ignored on non-Gw node: %v",
				constants.RouteAgentHostNetworkTableID, err)
			return
		}
	} else if kp.isGatewayNode && kp.cniIface != nil {
		// These routing rules are required ONLY on the Gateway Node.
		// On the non-Gateway nodes, we use iptable rules to support this use-case.
		switch operation {
		case Add:
			for _, inputCidrBlock := range inputCidrBlocks {
				if kp.routeCacheGWNode.Add(inputCidrBlock) {
					if err := kp.configureRoute(inputCidrBlock, operation); err != nil {
						kp.routeCacheGWNode.Remove(inputCidrBlock)
						klog.Errorf("Failed to add route %q for HostNetwork support on the Gateway node: %v",
							inputCidrBlock, err)
					}
				}
			}
		case Delete:
			for _, inputCidrBlock := range inputCidrBlocks {
				if kp.routeCacheGWNode.Remove(inputCidrBlock) {
					if err := kp.configureRoute(inputCidrBlock, operation); err != nil {
						klog.Errorf("Failed to delete route %q for HostNetwork support on the Gateway node. %v",
							inputCidrBlock, err)
					}
				}
			}
		}
	}
}

func (kp *SyncHandler) configureRoute(remoteSubnet string, operation Operation) error {
	src := net.ParseIP(kp.cniIface.IPAddress)
	_, dst, err := net.ParseCIDR(remoteSubnet)
	if err != nil {
		return fmt.Errorf("error parsing cidr block %s: %v", remoteSubnet, err)
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
		Scope:     unix.RT_SCOPE_LINK,
		LinkIndex: ifaceIndex,
		Protocol:  4,
		Table:     constants.RouteAgentHostNetworkTableID,
	}

	switch operation {
	case Add:
		err = netlink.RouteAdd(&route)
		if err != nil && !os.IsExist(err) {
			return fmt.Errorf("error adding the route %s: %v", route, err)
		}
	case Delete:
		err = netlink.RouteDel(&route)
		if err != nil {
			return fmt.Errorf("error deleting the route %s: %v", route, err)
		}
	}

	return nil
}

func (kp *SyncHandler) cleanVxSubmarinerRoutes() {
	link, err := netlink.LinkByName(VxLANIface)
	if err != nil {
		if _, ok := err.(netlink.LinkNotFoundError); !ok {
			klog.Errorf("Error retrieving link by name %q: %v", VxLANIface, err)
			return
		}
	}

	currentRouteList, err := netlink.RouteList(link, syscall.AF_INET)
	if err != nil {
		klog.Errorf("Unable to cleanup routes, error retrieving routes on the link %s: %v", VxLANIface, err)
		return
	}

	for i := range currentRouteList {
		klog.V(log.DEBUG).Infof("Processing route %v", currentRouteList[i])

		if currentRouteList[i].Dst == nil || currentRouteList[i].Gw == nil {
			klog.V(log.DEBUG).Infof("Found nil gw or dst")
		} else if kp.remoteSubnets.Contains(currentRouteList[i].Dst.String()) {
			klog.V(log.DEBUG).Infof("Removing route %s", currentRouteList[i])
			if err = netlink.RouteDel(&currentRouteList[i]); err != nil {
				klog.Errorf("Error removing route %s: %v", currentRouteList[i], err)
			}
		}
	}
}

// Reconcile the routes installed on this device using rtnetlink
func (kp *SyncHandler) reconcileRoutes(vxlanGw net.IP) error {
	klog.V(log.DEBUG).Infof("Reconciling routes to gw: %s", vxlanGw.String())

	link, err := netlink.LinkByName(VxLANIface)
	if err != nil {
		return fmt.Errorf("error retrieving link by name %s: %v", VxLANIface, err)
	}

	currentRouteList, err := netlink.RouteList(link, syscall.AF_INET)

	if err != nil {
		return fmt.Errorf("error retrieving routes for link %s: %v", VxLANIface, err)
	}

	// First lets delete all of the routes that don't match
	for i := range currentRouteList {
		// contains(endpoint destinations, route destination string, and the route gateway is our actual destination
		klog.V(log.DEBUG).Infof("Processing route %v", currentRouteList[i])

		if currentRouteList[i].Dst == nil || currentRouteList[i].Gw == nil {
			klog.V(log.DEBUG).Infof("Found nil gw or dst")
		} else {
			if kp.remoteSubnets.Contains(currentRouteList[i].Dst.String()) && currentRouteList[i].Gw.Equal(vxlanGw) {
				klog.V(log.DEBUG).Infof("Found route %s with gw %s already installed", currentRouteList[i], currentRouteList[i].Gw)
			} else {
				klog.V(log.DEBUG).Infof("Removing route %s", currentRouteList[i])
				if err = netlink.RouteDel(&currentRouteList[i]); err != nil {
					klog.Errorf("Error removing route %s: %v", currentRouteList[i], err)
				}
			}
		}
	}

	currentRouteList, err = netlink.RouteList(link, syscall.AF_INET)

	if err != nil {
		return fmt.Errorf("error retrieving routes for link %s: %v", VxLANIface, err)
	}

	// let's now add the routes that are missing
	for _, cidrBlock := range kp.remoteSubnets.Elements() {
		_, dst, err := net.ParseCIDR(cidrBlock)
		if err != nil {
			klog.Errorf("Error parsing cidr block %s: %v", cidrBlock, err)
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
		for _, curRoute := range currentRouteList {
			if curRoute.Gw == nil || curRoute.Dst == nil {

			} else if curRoute.Gw.Equal(route.Gw) && curRoute.Dst.String() == route.Dst.String() {
				klog.V(log.DEBUG).Infof("Found equivalent route, not adding")
				found = true
			}
		}

		if !found {
			err = netlink.RouteAdd(&route)
			if err != nil {
				klog.Errorf("Error adding route %s: %v", route, err)
			}
		}
	}

	return nil
}

func (kp *SyncHandler) updateRoutingRulesForInterClusterSupport(remoteCIDRs []string, operation Operation) error {
	if kp.isGatewayNode {
		klog.V(log.DEBUG).Info("On GWNode, in updateRoutingRulesForInterClusterSupport ignoring")
		// These rules are required only on the nonGatewayNode.
		return nil
	}

	if kp.vxlanDevice != nil && kp.vxlanGwIP != nil {
		link, err := netlink.LinkByName(VxLANIface)
		if err != nil {
			return fmt.Errorf("error retrieving link by name %s: %v", VxLANIface, err)
		}

		for _, cidrBlock := range remoteCIDRs {
			_, dst, err := net.ParseCIDR(cidrBlock)
			if err != nil {
				return fmt.Errorf("error parsing cidr block %s: %v", cidrBlock, err)
			}

			route := netlink.Route{
				Dst:       dst,
				Gw:        *kp.vxlanGwIP,
				Scope:     unix.RT_SCOPE_UNIVERSE,
				LinkIndex: link.Attrs().Index,
				Protocol:  4,
			}

			if operation == Add {
				err = netlink.RouteAdd(&route)
				if err != nil {
					return fmt.Errorf("error adding route %s: %v", route, err)
				}
			} else if operation == Delete {
				err = netlink.RouteDel(&route)
				if err != nil {
					return fmt.Errorf("error deleting route %s: %v", route, err)
				}
			}
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
			err := netlink.RuleAdd(rule)
			if err != nil && !os.IsExist(err) {
				return fmt.Errorf("failed to add ip rule %s: %v", rule, err)
			}
		case Delete:
			err := netlink.RuleDel(rule)
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("failed to delete ip rule %s: %v", rule, err)
			}
		}
	}

	return nil
}
