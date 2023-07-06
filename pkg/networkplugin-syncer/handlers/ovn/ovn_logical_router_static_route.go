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
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/libovsdbops"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/nbdb"
	"github.com/pkg/errors"
	"k8s.io/utils/set"
)

func (ovn *SyncHandler) reconcileSubOvnLogicalRouterStaticRoutes(port, nextHop string, remoteSubnets set.Set[string]) error {
	staleLRSRPred := func(item *nbdb.LogicalRouterStaticRoute) bool {
		return item.OutputPort != nil && *item.OutputPort == port && !remoteSubnets.Has(item.IPPrefix)
	}

	err := libovsdbops.DeleteLogicalRouterStaticRoutesWithPredicate(ovn.nbdb, submarinerLogicalRouter, staleLRSRPred)
	if err != nil {
		return errors.Wrapf(err, "Failed to list existing ovn logical route static routes for port: %s", port)
	}

	lrsrToAdd := buildLRSRsFromSubnets(remoteSubnets.UnsortedList(), port, nextHop)

	for _, lrsr := range lrsrToAdd {
		LRSRPred := func(item *nbdb.LogicalRouterStaticRoute) bool {
			return item.OutputPort != nil && *item.OutputPort == port && item.IPPrefix == lrsr.IPPrefix
		}

		err = libovsdbops.CreateOrUpdateLogicalRouterStaticRoutesWithPredicate(ovn.nbdb, submarinerLogicalRouter, lrsr, LRSRPred)
		if err != nil {
			return errors.Wrap(err, "Failed to create ovn lrsr and add it to the ovn submariner router")
		}
	}

	return nil
}

func buildLRSRsFromSubnets(subnetsToAdd []string, outPort, nextHop string) []*nbdb.LogicalRouterStaticRoute {
	toAdd := []*nbdb.LogicalRouterStaticRoute{}

	for _, subnet := range subnetsToAdd {
		toAdd = append(toAdd, &nbdb.LogicalRouterStaticRoute{
			OutputPort: &outPort,
			Nexthop:    nextHop,
			IPPrefix:   subnet,
		})
	}

	return toAdd
}
