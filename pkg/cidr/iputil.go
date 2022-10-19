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

package cidr

import (
	"fmt"
	"net"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var logger = log.Logger{Logger: logf.Log.WithName("CIDR")}

func OverlappingSubnets(localServiceCIDRs, localPodCIDRs, remoteSubnets []string) error {
	// If the remoteSubnets [*] overlap with local cluster Pod/Service CIDRs we
	// should not update the IPTable rules on the host, as it will disrupt the
	// functionality of the local cluster. So, lets validate that subnets do not
	// overlap before we program any IPTable rules on the host for inter-cluster
	// traffic.
	// [*] Note: In a non-GlobalNet deployment, remoteSubnets will be a list of
	// Pod/Service CIDRs, whereas in a GlobalNet deployment, it will be a list of
	// globalCIDRs allocated to the clusters.
	for _, serviceCidr := range localServiceCIDRs {
		overlap, err := IsOverlapping(remoteSubnets, serviceCidr)
		if err != nil {
			// Ideally this case will never hit, as the subnets are valid CIDRs
			logger.Warningf("Unable to validate overlapping Service CIDR: %s", err)
		}

		if overlap {
			return fmt.Errorf("local Service CIDR %q, overlaps with remote cluster subnets %s",
				serviceCidr, remoteSubnets)
		}
	}

	for _, podCidr := range localPodCIDRs {
		overlap, err := IsOverlapping(remoteSubnets, podCidr)
		if err != nil {
			logger.Warningf("Unable to validate overlapping Pod CIDR: %s", err)
		}

		if overlap {
			return fmt.Errorf("local Pod CIDR %q, overlaps with remote cluster subnets %s",
				podCidr, remoteSubnets)
		}
	}

	return nil
}

func IsOverlapping(cidrList []string, cidr string) (bool, error) {
	_, newNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return false, errors.Wrapf(err, "error parsing CIDR %q", cidr)
	}

	for _, v := range cidrList {
		_, baseNet, err := net.ParseCIDR(v)
		if err != nil {
			return false, errors.Wrapf(err, "error parsing CIDR %q", v)
		}

		if baseNet.Contains(newNet.IP) || newNet.Contains(baseNet.IP) {
			return true, nil
		}
	}

	return false, nil
}
