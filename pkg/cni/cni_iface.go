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

package cni

import (
	"net"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	k8snet "k8s.io/utils/net"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type Interface struct {
	Name      string
	IPAddress string
}

type HostInterface struct {
	Name string
	Addr string
}

var logger = log.Logger{Logger: logf.Log.WithName("CNI")}

var HostInterfaces = func() ([]HostInterface, error) {
	netInterfaces, err := net.Interfaces()
	if err != nil {
		return nil, errors.Wrapf(err, "net.Interfaces() returned error")
	}

	var hostInterfaces []HostInterface

	for i := range netInterfaces {
		addrs, err := netInterfaces[i].Addrs()
		if err != nil {
			return nil, errors.Wrapf(err, "for interface %q, iface.Addrs returned error", netInterfaces[i].Name)
		}

		for _, a := range addrs {
			hostInterfaces = append(hostInterfaces, HostInterface{Name: netInterfaces[i].Name, Addr: a.String()})
		}
	}

	return hostInterfaces, nil
}

func Discover(clusterCIDRs []string, family k8snet.IPFamily) (*Interface, error) {
	hostInterfaces, err := HostInterfaces()
	if err != nil {
		return nil, err
	}

	for _, clusterCIDR := range clusterCIDRs {
		if k8snet.IPFamilyOfCIDRString(clusterCIDR) != family {
			continue
		}

		_, clusterNetwork, err := net.ParseCIDR(clusterCIDR)
		utilruntime.Must(errors.Wrapf(err, "unable to ParseCIDR %q", clusterCIDR))

		for i := range hostInterfaces {
			ipAddr, _, err := net.ParseCIDR(hostInterfaces[i].Addr)
			if err != nil {
				logger.Errorf(err, "Unable to parse CIDR %q for host interface %q", hostInterfaces[i].Addr, hostInterfaces[i].Name)
				continue
			}

			if k8snet.IPFamilyOf(ipAddr) != family {
				continue
			}

			logger.V(log.DEBUG).Infof("Host interface %q has address %q", hostInterfaces[i].Name, ipAddr)
			address := net.ParseIP(ipAddr.String())

			// Verify that interface has an address from cluster CIDR
			if clusterNetwork.Contains(address) {
				logger.V(log.DEBUG).Infof("Found CNI Interface %q that has IP %q from ClusterCIDR %q",
					hostInterfaces[i].Name, ipAddr, clusterCIDR)
				return &Interface{IPAddress: ipAddr.String(), Name: hostInterfaces[i].Name}, nil
			}
		}
	}

	return nil, errors.Errorf("unable to find a CNI Interface which has an IP from the cluster CIDRs: %q", clusterCIDRs)
}
