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
	"k8s.io/apimachinery/pkg/util/sets"
)

// getNorthSubnetsToAddAndRemove receives the existing state for the north (other clusters) routes in the OVN
// database, and based on the known remote endpoints it will return the elements that need
// to be added and removed.
func (ovn *SyncHandler) getNorthSubnetsToAddAndRemove(existingSubnets sets.Set[string]) ([]string, []string) {
	newSubnets := ovn.remoteEndpointSubnetSet()

	toRemove := existingSubnets.Difference(newSubnets).UnsortedList()
	toAdd := newSubnets.Difference(existingSubnets).UnsortedList()

	return toAdd, toRemove
}

// remoteEndpointSubnetSet iterates over all known remote endpoints and subnets constructing a set of strings with
// all the remote subnets.
func (ovn *SyncHandler) remoteEndpointSubnetSet() sets.Set[string] {
	remoteSubnets := sets.New[string]()

	for _, endpoint := range ovn.remoteEndpoints {
		for _, subnet := range endpoint.Spec.Subnets {
			remoteSubnets.Insert(subnet)
		}
	}

	return remoteSubnets
}

// getSouthSubnetsToAddAndRemove receives the existing state for the south (our cluster) routes in the OVN
// submariner_router, and based on the known remote endpoints it will return the elements that need
// to be added and removed.
func (ovn *SyncHandler) getSouthSubnetsToAddAndRemove(existingSubnets sets.Set[string]) ([]string, []string) {
	newSubnets := ovn.localEndpointSubnetSet()

	toRemove := existingSubnets.Difference(newSubnets).UnsortedList()
	toAdd := newSubnets.Difference(existingSubnets).UnsortedList()

	return toAdd, toRemove
}

// remoteEndpointSubnetSet returns a set of strings with all the local subnets for this cluster based on the local endpoint
// information.
func (ovn *SyncHandler) localEndpointSubnetSet() sets.Set[string] {
	localSubnets := sets.New[string]()

	if ovn.localEndpoint != nil {
		for _, subnet := range ovn.localEndpoint.Spec.Subnets {
			localSubnets.Insert(subnet)
		}
	}

	return localSubnets
}
