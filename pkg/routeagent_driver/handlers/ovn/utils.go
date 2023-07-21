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

package ovn

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
)

func getNextHopOnK8sMgmtIntf() (string, error) {
	link, err := netlink.LinkByName(OVNK8sMgmntIntfName)
	if err != nil {
		return "", errors.Wrapf(err, "failed to retrieve link by name")
	}

	addrs, err := netlink.AddrList(link, 0)
	if err != nil || len(addrs) == 0 {
		return "", errors.Wrapf(err, "failed to retrieve addresses for link")
	}

	for _, addr := range addrs {
		if addr.IPNet != nil {
			ok, err := isIPv4CIDR(addr.IPNet.String())
			if err != nil {
				return "", err
			} else if ok {
				return addr.IPNet.IP.String(), nil
			}
		}
	}

	return "", nil
}

func isIPv4CIDR(address string) (bool, error) {
	_, iPnet, err := net.ParseCIDR(address)
	if err != nil {
		return false, errors.Wrapf(err, "Error parsing IP address %v", iPnet)
	}

	ip := iPnet.IP

	return ip != nil && ip.To4() != nil, nil
}

func jsonToIP(jsonData string) (string, error) {
	var data map[string]string

	err := json.Unmarshal([]byte(jsonData), &data)
	if err != nil {
		return "", errors.Wrapf(err, "error marshalling the json ip")
	}

	ipStr, found := data["ipv4"]
	if !found {
		return "", fmt.Errorf("json data does not contain an 'ipv4' field")
	}

	ip, _, err := net.ParseCIDR(ipStr)
	if err != nil {
		return "", fmt.Errorf("invalid IP CIDR address: %s", ipStr)
	}

	return ip.String(), nil
}
