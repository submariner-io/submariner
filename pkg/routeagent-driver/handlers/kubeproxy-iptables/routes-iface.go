package kp_iptables

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"k8s.io/klog"

	"github.com/submariner-io/admiral/pkg/log"

	"github.com/submariner-io/submariner/pkg/cable/wireguard"
)

func (kp *SyncHandler) updateRoutingRulesForHostNetworkSupport(inputCidrBlocks []string, operation Operation) {
	if operation == FlushRouteTable {
		kp.routeCacheGWNode.DeleteAll()
		// The conversion doesn't introduce a security problem
		// #nosec G204
		cmd := exec.Command("/sbin/ip", "r", "flush", "table", strconv.Itoa(RouteAgentHostNetworkTableID))
		if err := cmd.Run(); err != nil {
			// We can safely ignore this error, as this table will exist only on GW nodes
			klog.V(log.TRACE).Infof("Flushing routing table %d returned error. Can be ignored on non-Gw node: %v",
				RouteAgentHostNetworkTableID, err)
			return
		}
	} else if kp.isGatewayNode && kp.cniIface != nil {
		// These routing rules are required ONLY on the Gateway Node.
		// On the non-Gateway nodes, we use iptable rules to support this use-case.
		switch operation {
		case AddRoute:
			for _, inputCidrBlock := range inputCidrBlocks {
				if kp.routeCacheGWNode.Add(inputCidrBlock) {
					if err := kp.configureRoute(inputCidrBlock, operation); err != nil {
						kp.routeCacheGWNode.Delete(inputCidrBlock)
						klog.Errorf("Failed to add route %q for HostNetwork support on the Gateway node: %v",
							inputCidrBlock, err)
					}
				}
			}
		case DeleteRoute:
			for _, inputCidrBlock := range inputCidrBlocks {
				if kp.routeCacheGWNode.Delete(inputCidrBlock) {
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
		Table:     RouteAgentHostNetworkTableID,
	}

	switch operation {
	case AddRoute:
		err = netlink.RouteAdd(&route)
		if err != nil && !os.IsExist(err) {
			return fmt.Errorf("error adding the route %s: %v", route.String(), err)
		}
	case DeleteRoute:
		err = netlink.RouteDel(&route)
		if err != nil {
			return fmt.Errorf("error deleting the route %s: %v", route.String(), err)
		}
	}

	return nil
}
