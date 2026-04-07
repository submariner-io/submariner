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
	"net"

	"github.com/pkg/errors"
	"github.com/submariner-io/submariner/pkg/cni"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	k8snet "k8s.io/utils/net"
)

var (
	familyToDestIP = map[k8snet.IPFamily]string{
		k8snet.IPv4: "8.8.8.8",
		k8snet.IPv6: "2001:4860:4860::8888",
	}

	// Holds IPv6 address reserved by OVN-Kubernetes for internal usage.
	infraInternalIPv6 = net.ParseIP("fd69::2")

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
			if family == k8snet.IPv6 && !isAcceptableIPv6(ip) {
				continue
			}

			return ip.String(), nil
		}
	}

	return "", errors.Errorf("couldn't find route for family %v", family)
}

func GetLocalIPForDestination(dst string, family k8snet.IPFamily) (string, error) {
	conn, err := Dial("udp"+string(family), net.JoinHostPort(dst, "53"))
	if err == nil {
		defer conn.Close()

		localAddr := conn.LocalAddr().(*net.UDPAddr)
		if family == k8snet.IPv4 || isAcceptableIPv6(localAddr.IP) {
			return localAddr.IP.String(), nil
		}

		logger.Infof("IP %q returned from Dial isn't usable - trying to find another IP from the same interface", localAddr.IP)

		// Try to find a valid global-scope IP from the same interface
		ifaceIP, err := getValidGlobalIPv6FromSameInterface(localAddr.IP)
		if err != nil {
			logger.Errorf(err, "Error retrieving valid IPv6 address for %q", localAddr.IP)
		} else if ifaceIP != "" {
			return ifaceIP, nil
		} else {
			logger.Infof("No acceptable IPv6 found on same interface as %q, trying route-based discovery", localAddr.IP)
		}
	}

	// connection failed try fallback method
	localIP, err := getLocalIPFromRoutes(family)

	return localIP, errors.Wrapf(err, "error getting local IPv%v", family)
}

func isAcceptableIPv6(ip net.IP) bool {
	ip = ip.To16()

	return ip != nil &&
		!ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.Equal(infraInternalIPv6)
}

func getValidGlobalIPv6FromSameInterface(ip net.IP) (string, error) {
	hostInterfaces, err := cni.HostInterfaces()
	if err != nil {
		return "", errors.Wrap(err, "failed to list interfaces")
	}

	var ownedIfaceName string

	for i := range hostInterfaces {
		actualIP := hostInterfaces[i].Addr

		if k8snet.IPFamilyOf(actualIP) != k8snet.IPv6 {
			continue
		}

		if ownedIfaceName == "" && actualIP.Equal(ip) {
			ownedIfaceName = hostInterfaces[i].Name
		}

		if ownedIfaceName != "" && ownedIfaceName == hostInterfaces[i].Name && isAcceptableIPv6(actualIP) {
			return actualIP.String(), nil
		}
	}

	return "", nil
}

func GetLocalIP(family k8snet.IPFamily) (string, error) {
	return GetLocalIPForDestination(familyToDestIP[family], family)
}
