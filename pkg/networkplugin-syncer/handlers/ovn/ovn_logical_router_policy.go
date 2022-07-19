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
	"strings"

	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/libovsdbops"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/nbdb"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/stringset"
)

func (ovn *SyncHandler) reconcileSubOvnLogicalRouterPolicies(remoteSubnets stringset.Interface) error {
	lrpStalePredicate := func(item *nbdb.LogicalRouterPolicy) bool {
		subnet := strings.Split(item.Match, " ")[2]

		return item.Priority == ovnRoutePoliciesPrio && !remoteSubnets.Contains(subnet)
	}

	// Cleanup any existing lrps not representing the correct set of remote subnets
	err := libovsdbops.DeleteLogicalRouterPoliciesWithPredicate(ovn.nbdb, ovnClusterRouter, lrpStalePredicate)
	if err != nil {
		return errors.Wrapf(err, "failed to delete stale submariner logical route policies")
	}

	expectedLRPs := buildLRPsFromSubnets(remoteSubnets.Elements())

	for _, lrp := range expectedLRPs {
		lrpSubPredicate := func(item *nbdb.LogicalRouterPolicy) bool {
			subnet1 := strings.Split(item.Match, " ")[2]
			subnet2 := strings.Split(lrp.Match, " ")[2]

			return item.Priority == ovnRoutePoliciesPrio && subnet1 == subnet2
		}

		if err := libovsdbops.CreateOrUpdateLogicalRouterPolicyWithPredicate(ovn.nbdb,
			ovnClusterRouter, lrp, lrpSubPredicate); err != nil {
			return errors.Wrapf(err, "failed to create submariner logical Router policy %v and add it to the ovn cluster router", lrp)
		}
	}

	return nil
}

// getNorthSubnetsToAddAndRemove receives the existing state for the north (other clusters) routes in the OVN
// database as an StringSet, and based on the known remote endpoints it will return the elements that need
// to be added and removed.
func buildLRPsFromSubnets(subnetsToAdd []string) []*nbdb.LogicalRouterPolicy {
	tmpDownstreamIP := submarinerDownstreamIP
	toAdd := []*nbdb.LogicalRouterPolicy{}

	for _, subnet := range subnetsToAdd {
		toAdd = append(toAdd, &nbdb.LogicalRouterPolicy{
			Priority: ovnRoutePoliciesPrio,
			Action:   "reroute",
			Match:    "ip4.dst == " + subnet,
			Nexthop:  &tmpDownstreamIP,
			ExternalIDs: map[string]string{
				"submariner": "true",
			},
		})
	}

	return toAdd
}
