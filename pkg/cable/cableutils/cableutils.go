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

package cableutils

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/vishvananda/netlink"
	"k8s.io/klog"
)

// Parse CIDR string and skip errors.
func ParseSubnets(subnets []string) []net.IPNet {
	nets := make([]net.IPNet, 0, len(subnets))

	for _, sn := range subnets {
		_, cidr, err := net.ParseCIDR(sn)
		if err != nil {
			// this should not happen. Log and continue
			klog.Errorf("failed to parse subnet %s: %v", sn, err)
			continue
		}

		nets = append(nets, *cidr)
	}

	return nets
}

func AddIPRule(tableID int) error {
	rule := netlink.NewRule()
	rule.Table = tableID
	rule.Priority = tableID

	err := netlink.RuleAdd(rule)
	if err != nil && !os.IsExist(err) {
		return errors.Wrapf(err, "failed to add ip rule %s", rule)
	}

	return nil
}

func GetTunnelEndPointIPAddress(ipAddr string, prefix int) (net.IP, error) {
	ipSlice := strings.Split(ipAddr, ".")
	if len(ipSlice) < 4 {
		return nil, fmt.Errorf("invalid ipAddr [%s]", ipAddr)
	}

	ipSlice[0] = strconv.Itoa(prefix)
	tunnelIP := net.ParseIP(strings.Join(ipSlice, "."))

	return tunnelIP, nil
}

func AddRoute(ipAddressList []net.IPNet, gwIP, ip net.IP, linkIdx, tableID int) error {
	for i := range ipAddressList {
		route := &netlink.Route{
			LinkIndex: linkIdx,
			Src:       ip,
			Dst:       &ipAddressList[i],
			Gw:        gwIP,
			Type:      netlink.NDA_DST,
			Flags:     netlink.NTF_SELF,
			Priority:  100,
			Table:     tableID,
		}
		err := netlink.RouteAdd(route)

		if errors.Is(err, syscall.EEXIST) {
			err = netlink.RouteReplace(route)
		}

		if err != nil {
			return errors.Wrapf(err, "unable to add the route entry %v", route)
		}

		klog.V(log.DEBUG).Infof("Successfully added the route entry %v and gw ip %v", route, gwIP)
	}

	return nil
}

func DelRoute(ipAddressList []net.IPNet, linkIdx, tableID int) error {
	for i := range ipAddressList {
		route := &netlink.Route{
			LinkIndex: linkIdx,
			Dst:       &ipAddressList[i],
			Gw:        nil,
			Type:      netlink.NDA_DST,
			Flags:     netlink.NTF_SELF,
			Priority:  100,
			Table:     tableID,
		}

		err := netlink.RouteDel(route)
		if err != nil {
			return errors.Wrapf(err, "unable to add the route entry %v", route)
		}

		klog.V(log.DEBUG).Infof("Successfully deleted the route entry %v", route)
	}

	return nil
}

func ConfigureIPAddress(ipAddress net.IP, mask net.IPMask, link netlink.Link, ifname string) error {
	ipConfig := &netlink.Addr{IPNet: &net.IPNet{
		IP:   ipAddress,
		Mask: mask,
	}}

	err := netlink.AddrAdd(link, ipConfig)
	if errors.Is(err, syscall.EEXIST) {
		return nil
	} else if err != nil {
		return errors.Wrapf(err, "unable to configure address (%s) on interface (%s)", ipAddress, ifname)
	}

	return nil
}

func RemoveConnectionForEndpoint(connections []v1.Connection, endpoint *types.SubmarinerEndpoint) []v1.Connection {
	for j := range connections {
		if connections[j].Endpoint.CableName == endpoint.Spec.CableName {
			copy(connections[j:], connections[j+1:])
			return connections[:len(connections)-1]
		}
	}

	return connections
}
