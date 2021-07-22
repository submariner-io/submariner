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

package util

import (
	"fmt"
	"log"
	"net"
	"strings"
	"syscall"
	"unicode"

	"github.com/vishvananda/netlink"
	"k8s.io/apimachinery/pkg/api/equality"

	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/types"
)

const tokenLength = 64

func getAPIIdentifier(token string) (string, error) {
	if len(token) != tokenLength {
		return "", fmt.Errorf("token %s length was not %d", token, tokenLength)
	}

	clusterID := token[0 : tokenLength/2]

	return clusterID, nil
}

func getConnectSecret(token string) (string, error) {
	if len(token) != tokenLength {
		return "", fmt.Errorf("token %s length was not %d", token, tokenLength)
	}

	connectSecret := token[tokenLength/2 : tokenLength]

	return connectSecret, nil
}

func ParseSecure(token string) (types.Secure, error) {
	secretKey, err := getConnectSecret(token)
	if err != nil {
		return types.Secure{}, err
	}

	apiKey, err := getAPIIdentifier(token)
	if err != nil {
		return types.Secure{}, err
	}

	return types.Secure{
		APIKey:    apiKey,
		SecretKey: secretKey,
	}, nil
}

func GetLocalIP() string {
	return GetLocalIPForDestination("8.8.8.8")
}

func GetLocalIPForDestination(dst string) string {
	conn, err := net.Dial("udp", dst+":53")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP.String()
}

func FlattenColors(colorCodes []string) string {
	if len(colorCodes) == 0 {
		return ""
	}

	flattenedColors := colorCodes[0]

	for k, v := range colorCodes {
		if k != 0 {
			flattenedColors = flattenedColors + "," + v
		}
	}

	return flattenedColors
}

func GetClusterIDFromCableName(cableName string) string {
	// length is 11
	// 0           1    2   3    4    5       6   7  8 9  10
	// submariner-cable-my-super-long_cluster-id-172-16-32-5
	cableSplit := strings.Split(cableName, "-")
	clusterID := cableSplit[2]

	for i := 3; i < len(cableSplit)-4; i++ {
		clusterID = clusterID + "-" + cableSplit[i]
	}

	return clusterID
}

func GetEndpointCRDName(endpoint *types.SubmarinerEndpoint) (string, error) {
	return GetEndpointCRDNameFromParams(endpoint.Spec.ClusterID, endpoint.Spec.CableName)
}

func GetEndpointCRDNameFromParams(clusterID, cableName string) (string, error) {
	if clusterID == "" || cableName == "" {
		return "", fmt.Errorf("error, cluster ID or cable name was empty")
	}

	return EnsureValidName(fmt.Sprintf("%s-%s", clusterID, cableName)), nil
}

func GetClusterCRDName(cluster *types.SubmarinerCluster) (string, error) {
	if cluster.Spec.ClusterID == "" {
		return "", fmt.Errorf("ClusterID was empty")
	}

	return cluster.Spec.ClusterID, nil
}

func CompareEndpointSpec(left, right subv1.EndpointSpec) bool {
	// maybe we have to use just reflect.DeepEqual(left, right), but in this case the subnets order will influence.
	return left.ClusterID == right.ClusterID && left.CableName == right.CableName && left.Hostname == right.Hostname &&
		left.Backend == right.Backend && equality.Semantic.DeepEqual(left.BackendConfig, right.BackendConfig)
}

func GetDefaultGatewayInterface() (*net.Interface, error) {
	routes, err := netlink.RouteList(nil, syscall.AF_INET)
	if err != nil {
		return nil, err
	}

	for _, route := range routes {
		if route.Dst == nil || route.Dst.String() == "0.0.0.0/0" {
			if route.LinkIndex == 0 {
				return nil, fmt.Errorf("default gateway interface could not be determined")
			}

			iface, err := net.InterfaceByIndex(route.LinkIndex)
			if err != nil {
				return nil, err
			}

			return iface, nil
		}
	}

	return nil, fmt.Errorf("unable to find default route")
}

func EnsureValidName(name string) string {
	// K8s only allows lower case alphanumeric characters, '-' or '.'. Regex used for validation is
	// '[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*'
	return strings.Map(func(c rune) rune {
		c = unicode.ToLower(c)
		if !unicode.IsDigit(c) && !unicode.IsLower(c) && c != '-' && c != '.' {
			return '-'
		}

		return c
	}, name)
}
