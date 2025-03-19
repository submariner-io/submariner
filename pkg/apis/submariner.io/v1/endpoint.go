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

package v1

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/submariner/pkg/cidr"
	"k8s.io/apimachinery/pkg/api/equality"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	k8snet "k8s.io/utils/net"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var logger = log.Logger{Logger: logf.Log.WithName("EndpointAPI")}

func (ep *EndpointSpec) GetBackendPort(configName string, defaultValue int32) (int32, error) {
	if portStr := ep.BackendConfig[configName]; portStr != "" {
		port, err := parsePort(portStr)
		if err != nil {
			return defaultValue, errors.Wrapf(err, "error parsing backend config %s", configName)
		}

		return port, nil
	}

	return defaultValue, nil
}

func (ep *EndpointSpec) GetBackendBool(configName string, defaultValue *bool) (*bool, error) {
	if boolStr := ep.BackendConfig[configName]; boolStr != "" {
		boolValue, err := strconv.ParseBool(boolStr)
		if err != nil {
			return defaultValue, errors.Wrapf(err, "error parsing backend config %s", configName)
		}

		return &boolValue, nil
	}

	return defaultValue, nil
}

func parsePort(port string) (int32, error) {
	portInt, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return -1, errors.Wrapf(err, "error parsing port %s", port)
	} else if portInt < 1 {
		return -1, errors.Errorf("port %s is < 1", port)
	} else if portInt > 65535 {
		return -1, errors.Errorf("port %s is > 65535", port)
	}

	return int32(portInt), nil
}

func (ep *EndpointSpec) GenerateName() (string, error) {
	if ep.ClusterID == "" {
		return "", errors.New("ClusterID cannot be empty")
	}

	if ep.CableName == "" {
		return "", errors.New("CableName cannot be empty")
	}

	return resource.EnsureValidName(fmt.Sprintf("%s-%s", ep.ClusterID, ep.CableName)), nil
}

func (ep *EndpointSpec) Equals(other *EndpointSpec) bool {
	if ep == nil && other == nil {
		return true
	}

	if ep == nil || other == nil {
		return false
	}

	return ep.ClusterID == other.ClusterID && ep.CableName == other.CableName && ep.Hostname == other.Hostname &&
		ep.Backend == other.Backend && ep.hasSameBackendConfig(other)
}

func (ep *EndpointSpec) hasSameBackendConfig(other *EndpointSpec) bool {
	if ep.BackendConfig[UsingLoadBalancer] == "true" &&
		other.BackendConfig[UsingLoadBalancer] == "true" {
		// When Gateway pod comes up with loadbalancer mode enabled, it inserts a preferred-server-timestamp in
		// the BackendConfig when the Gateway pod comes up. So, in loadbalancer mode, we just have to compare
		// the load-balancer status.
		return true
	}

	return equality.Semantic.DeepEqual(ep.BackendConfig, other.BackendConfig)
}

func getIPFrom(family k8snet.IPFamily, ips []string, ipv4Fallback string) string {
	for _, ip := range ips {
		if k8snet.IPFamilyOfString(ip) == family {
			return ip
		}
	}

	if family == k8snet.IPv4 {
		return ipv4Fallback
	}

	return ""
}

func setIP(ips []string, ipv4Fallback, newIP string) ([]string, string) {
	if newIP == "" {
		return ips, ipv4Fallback
	}

	family := k8snet.IPFamilyOfString(newIP)
	if family == k8snet.IPFamilyUnknown {
		logger.Errorf(nil, "Unable to determine IP family for %q - ignoring", newIP)
		return ips, ipv4Fallback
	}

	if family == k8snet.IPv4 {
		ipv4Fallback = newIP
	}

	for i := range ips {
		if k8snet.IPFamilyOfString(ips[i]) == family {
			ips[i] = newIP
			return ips, ipv4Fallback
		}
	}

	ips = append(ips, newIP)

	return ips, ipv4Fallback
}

func (ep *EndpointSpec) GetHealthCheckIP(family k8snet.IPFamily) string {
	return getIPFrom(family, ep.HealthCheckIPs, ep.HealthCheckIP)
}

func (ep *EndpointSpec) SetHealthCheckIP(ip string) {
	ep.HealthCheckIPs, ep.HealthCheckIP = setIP(ep.HealthCheckIPs, ep.HealthCheckIP, ip)
}

func (ep *EndpointSpec) GetPublicIP(family k8snet.IPFamily) string {
	return getIPFrom(family, ep.PublicIPs, ep.PublicIP)
}

func (ep *EndpointSpec) SetPublicIP(ip string) {
	ep.PublicIPs, ep.PublicIP = setIP(ep.PublicIPs, ep.PublicIP, ip)
}

func (ep *EndpointSpec) GetPrivateIP(family k8snet.IPFamily) string {
	return getIPFrom(family, ep.PrivateIPs, ep.PrivateIP)
}

func (ep *EndpointSpec) SetPrivateIP(ip string) {
	ep.PrivateIPs, ep.PrivateIP = setIP(ep.PrivateIPs, ep.PrivateIP, ip)
}

func (ep *EndpointSpec) GetIPFamilies() []k8snet.IPFamily {
	return cidr.ExtractIPFamilies(ep.Subnets)
}

func (ep *EndpointSpec) GetFamilyCableName(family k8snet.IPFamily) string {
	return ep.CableName + "-v" + string(family)
}

func (ep *EndpointSpec) ParseSubnets(family k8snet.IPFamily) []net.IPNet {
	var subnets []net.IPNet

	for i := range ep.Subnets {
		_, cidr, err := net.ParseCIDR(ep.Subnets[i])
		utilruntime.Must(err)

		if k8snet.IPFamilyOfCIDR(cidr) == family {
			subnets = append(subnets, *cidr)
		}
	}

	return subnets
}

func (ep *EndpointSpec) ExtractSubnetsExcludingIP(excIP string) []string {
	var subnets []string

	ipFamily := k8snet.IPFamilyOfString(excIP)
	subnetPrefix := excIP + "/"

	for _, subnet := range ep.ParseSubnets(ipFamily) {
		subnetStr := subnet.String()
		if !strings.HasPrefix(subnetStr, subnetPrefix) && k8snet.IPFamilyOfCIDRString(subnetStr) == ipFamily {
			subnets = append(subnets, subnetStr)
		}
	}

	return subnets
}
