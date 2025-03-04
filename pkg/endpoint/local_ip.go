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

	k8snet "k8s.io/utils/net"
)

var familyToDestIP = map[k8snet.IPFamily]string{
	k8snet.IPv4: "8.8.8.8",
	k8snet.IPv6: "2001:4860:4860::8888",
}

func getLocalIPv4ForDestination(dst string) string {
	conn, err := net.Dial("udp4", dst+":53")
	logger.FatalOnError(err, "Error getting local IP")

	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP.String()
}

func GetLocalIPForDestination(dst string, family k8snet.IPFamily) string {
	switch family {
	case k8snet.IPv4:
		return getLocalIPv4ForDestination(dst)
	case k8snet.IPv6:
		// TODO_IPV6: add V6 healthcheck IP
	case k8snet.IPFamilyUnknown:
	}

	return ""
}

func GetLocalIP(family k8snet.IPFamily) string {
	return GetLocalIPForDestination(familyToDestIP[family], family)
}
