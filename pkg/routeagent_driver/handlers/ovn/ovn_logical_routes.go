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
	"reflect"
	"strings"

	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/libovsdbops"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/nbdb"
	"github.com/pkg/errors"
	"github.com/submariner-io/submariner/pkg/versions"
	"k8s.io/apimachinery/pkg/util/sets"
	k8snet "k8s.io/utils/net"
	"k8s.io/utils/ptr"
)

const (
	OVNClusterRouter     = "ovn_cluster_router"
	ovnRoutePoliciesPrio = 20000
)

func (c *ConnectionHandler) reconcileOvnLogicalRouterStaticRoutes(remoteSubnets sets.Set[string],
	nextHop string,
) error {
	staleLRSRPred := func(item *nbdb.LogicalRouterStaticRoute) bool {
		// Legacy routes will have same prefix but different nextHop
		return (item.Nexthop == nextHop && !remoteSubnets.Has(item.IPPrefix)) ||
			(item.Nexthop != nextHop && remoteSubnets.Has(item.IPPrefix))
	}

	err := libovsdbops.DeleteLogicalRouterStaticRoutesWithPredicate(c.nbdb, OVNClusterRouter, staleLRSRPred)
	if err != nil {
		return errors.Wrapf(err, "failed to delete existing ovn logical route static routes for nexthop: %s", nextHop)
	}

	lrsrToAdd := buildLRSRsFromSubnets(remoteSubnets.UnsortedList(), nextHop)

	for _, lrsr := range lrsrToAdd {
		LRSRPred := func(item *nbdb.LogicalRouterStaticRoute) bool {
			return item.Nexthop == nextHop && item.IPPrefix == lrsr.IPPrefix
		}

		err = libovsdbops.CreateOrUpdateLogicalRouterStaticRoutesWithPredicate(c.nbdb, OVNClusterRouter, lrsr, LRSRPred)
		if err != nil {
			return errors.Wrap(err, "failed to create ovn lrsr and add it to the ovn submariner router")
		}
	}

	return nil
}

func buildLRSRsFromSubnets(subnetsToAdd []string, nextHop string) []*nbdb.LogicalRouterStaticRoute {
	toAdd := []*nbdb.LogicalRouterStaticRoute{}

	for _, subnet := range subnetsToAdd {
		toAdd = append(toAdd, &nbdb.LogicalRouterStaticRoute{
			Nexthop:  nextHop,
			IPPrefix: subnet,
			ExternalIDs: map[string]string{
				"submariner": versions.Submariner(),
			},
		})
	}

	return toAdd
}

func (c *ConnectionHandler) reconcileSubOvnLogicalRouterPolicies(remoteSubnets sets.Set[string], nextHop string) error {
	expectedLRPs := buildLRPsFromSubnets(c.ipFamily, remoteSubnets.UnsortedList(), nextHop)

	lrpStalePredicate := func(item *nbdb.LogicalRouterPolicy) bool {
		if item.Priority != ovnRoutePoliciesPrio {
			return false
		}

		parts := strings.Split(item.Match, " ")
		if len(parts) < 3 {
			return false
		}

		subnet := parts[2]

		return !remoteSubnets.Has(subnet) || !reflect.DeepEqual(item.Nexthop, &nextHop)
	}

	// Cleanup any existing lrps not representing the correct set of remote subnets
	if err := libovsdbops.DeleteLogicalRouterPoliciesWithPredicate(c.nbdb, OVNClusterRouter, lrpStalePredicate); err != nil {
		return errors.Wrapf(err, "failed to delete stale submariner logical route policies")
	}

	for _, lrp := range expectedLRPs {
		lrpSubPredicate := func(item *nbdb.LogicalRouterPolicy) bool {
			return item.Priority == lrp.Priority &&
				item.Match == lrp.Match &&
				reflect.DeepEqual(item.Nexthop, lrp.Nexthop)
		}

		if err := libovsdbops.CreateOrUpdateLogicalRouterPolicyWithPredicate(c.nbdb, OVNClusterRouter, lrp, lrpSubPredicate); err != nil {
			return errors.Wrapf(err, "failed to create submariner logical Router policy %v and add it to the ovn cluster router", lrp)
		}
	}

	return nil
}

// getNorthSubnetsToAddAndRemove receives the existing state for the north (other clusters) routes in the OVN
// database, and based on the known remote endpoints it will return the elements that need
// to be added and removed.
func buildLRPsFromSubnets(family k8snet.IPFamily, subnetsToAdd []string, nextHop string) []*nbdb.LogicalRouterPolicy {
	toAdd := make([]*nbdb.LogicalRouterPolicy, 0, len(subnetsToAdd))

	var ipMatchField string

	if family == k8snet.IPv6 {
		ipMatchField = "ip6.dst"
	} else {
		ipMatchField = "ip4.dst"
	}

	for _, subnet := range subnetsToAdd {
		match := ipMatchField + " == " + subnet

		toAdd = append(toAdd, &nbdb.LogicalRouterPolicy{
			Priority: ovnRoutePoliciesPrio,
			Action:   "reroute",
			Match:    match,
			Nexthop:  ptr.To(nextHop),
			ExternalIDs: map[string]string{
				"submariner": versions.Submariner(),
			},
		})
	}

	return toAdd
}
