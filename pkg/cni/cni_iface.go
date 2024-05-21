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
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type Interface struct {
	Name      string
	IPAddress string
}

var logger = log.Logger{Logger: logf.Log.WithName("CNI")}

// DiscoverFunc is a hook for unit tests.
var DiscoverFunc func(clusterCIDRs []string) (*Interface, error)

func Discover(clusterCIDRs []string) (*Interface, error) {
	if DiscoverFunc != nil {
		return DiscoverFunc(clusterCIDRs)
	}

	return discover(clusterCIDRs)
}

func discover(clusterCIDRs []string) (*Interface, error) {
	for _, clusterCIDR := range clusterCIDRs {
		_, clusterNetwork, err := net.ParseCIDR(clusterCIDR)
		if err != nil {
			return nil, errors.Wrapf(err, "unable to ParseCIDR %q", clusterCIDR)
		}

		hostInterfaces, err := net.Interfaces()
		if err != nil {
			return nil, errors.Wrapf(err, "net.Interfaces() returned error")
		}

		for _, iface := range hostInterfaces {
			addrs, err := iface.Addrs()
			if err != nil {
				return nil, errors.Wrapf(err, "for interface %q, iface.Addrs returned error", iface.Name)
			}

			for i := range addrs {
				ipAddr, _, err := net.ParseCIDR(addrs[i].String())
				if err != nil {
					logger.Errorf(err, "Unable to ParseCIDR : %q", addrs[i].String())
				} else if ipAddr.To4() != nil {
					logger.V(log.DEBUG).Infof("Interface %q has %q address", iface.Name, ipAddr)
					address := net.ParseIP(ipAddr.String())

					// Verify that interface has an address from cluster CIDR
					if clusterNetwork.Contains(address) {
						logger.V(log.DEBUG).Infof("Found CNI Interface %q that has IP %q from ClusterCIDR %q",
							iface.Name, ipAddr, clusterCIDR)
						return &Interface{IPAddress: ipAddr.String(), Name: iface.Name}, nil
					}
				}
			}
		}
	}

	return nil, errors.Errorf("unable to find CNI Interface on the host which has IP from %q", clusterCIDRs)
}
