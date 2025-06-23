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

package endpoint

import (
	"fmt"
	"net"

	"github.com/pkg/errors"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	k8snet "k8s.io/utils/net"
)

var (
	familyToDestIP = map[k8snet.IPFamily]string{
		k8snet.IPv4: "8.8.8.8",
		k8snet.IPv6: "2001:4860:4860::8888",
	}
	Dial = net.Dial
)

func getLocalIPFromRoutes(family k8snet.IPFamily) (string, error) {
	netlink := netlinkAPI.New()

	routes, err := netlink.RouteList(nil, family)
	if err != nil {
		return "", errors.Wrapf(err, "error listing routes for IPv%v", family)
	}

	for i := range routes {
		if routes[i].Gw != nil && routes[i].Src != nil {
			// OVNK configures special internal masquerade IPs,
			// these IPs are used internally by OVNK infra dataplane, and they should
			// not be selected as private IPs.
			ip := routes[i].Src
			if family == k8snet.IPv6 && !isValidGlobalIPv6(ip) {
				continue
			}

			return ip.String(), nil
		}
	}

	return "", errors.Errorf("couldn't find route for family %v", family)
}

func GetLocalIPForDestination(dst string, family k8snet.IPFamily) string {
	conn, err := Dial("udp"+string(family), net.JoinHostPort(dst, "53"))
	if err == nil {
		defer conn.Close()
		localAddr := conn.LocalAddr().(*net.UDPAddr)

		if family == k8snet.IPv4 || isValidGlobalIPv6(localAddr.IP) {
			return localAddr.IP.String()
		}

		logger.Warningf("IP %q returned from Dial isn't usable - trying to find another IP from the same interface", localAddr.IP)

		// Try to find a valid global-scope IP from the same interface
		ifaceIP, err := getValidGlobalIPv6FromSameInterface(localAddr.IP)
		if err == nil {
			return ifaceIP
		} else {
			logger.Warningf("Failed to retrieve valid IPv6 address for %q: %v", localAddr.IP, err)
		}
	}

	// connection failed try fallback method
	localIP, err := getLocalIPFromRoutes(family)
	logger.FatalOnError(err, fmt.Sprintf("Error getting local IPv%v", family))

	return localIP
}

func isValidGlobalIPv6(ip net.IP) bool {
	// fd00::/8 - avoid locally assigned ULA
	if ip.To16() == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip[0] == 0xfd {
		return false
	}
	// Accept fc00::/8 or global unicast (2000::/3)
	return ip[0] == 0xfc || (ip[0]&0xe0 == 0x20)
}

func getValidGlobalIPv6FromSameInterface(ip net.IP) (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", errors.Wrap(err, "failed to list interfaces")
	}
	var (
		ownedIfaceName  string
		ownedIfaceAddrs []net.Addr
	)

	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			logger.Warningf("Failed to list the address from the interface %v", iface.Name)
			continue
		}

		// Confirm this interface owns the original (non-global) IP
		ownsIP := false

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.To4() != nil {
				continue
			}

			if ipNet.Contains(ip) {
				ownsIP = true
				break
			}
		}

		if ownsIP {
			ownedIfaceName = iface.Name
			ownedIfaceAddrs = addrs

			break
		}
	}

	if len(ownedIfaceAddrs) == 0 {
		return "", fmt.Errorf("no interface for IP %q was found", ip)
	}
	// Scan for a valid global IPv6 on the same interface
	for _, addr := range ownedIfaceAddrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}

		if isValidGlobalIPv6(ipNet.IP) {
			return ipNet.IP.String(), nil
		}
	}

	return "", fmt.Errorf("no valid global IPv6 found on interface %q for IP %q", ownedIfaceName, ip)
}

func GetLocalIP(family k8snet.IPFamily) string {
	return GetLocalIPForDestination(familyToDestIP[family], family)
}
