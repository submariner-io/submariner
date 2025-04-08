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
		if routes[i].Gw != nil {
			return routes[i].Src.String(), nil
		}
	}

	return "", errors.Errorf("couldn't find route for family %v", family)
}

func GetLocalIPForDestination(dst string, family k8snet.IPFamily) string {
	conn, err := Dial("udp"+string(family), "["+dst+"]:53")
	if err == nil {
		defer conn.Close()
		localAddr := conn.LocalAddr().(*net.UDPAddr)

		return localAddr.IP.String()
	}

	// connection failed try fallback method
	localIP, err := getLocalIPFromRoutes(family)
	logger.FatalOnError(err, fmt.Sprintf("Error getting local IPv%v", family))

	return localIP
}

func GetLocalIP(family k8snet.IPFamily) string {
	return GetLocalIPForDestination(familyToDestIP[family], family)
}
