package ovn

import "github.com/submariner-io/submariner/pkg/util"

// getNorthSubnetsToAddAndRemove receives the existing state for the north (other clusters) routes in the OVN
// database as an StringSet, and based on the known remote endpoints it will return the elements that need
// to be added and removed.
func (ovn *SyncHandler) getNorthSubnetsToAddAndRemove(existingSubnets *util.StringSet) ([]string, []string) {
	newSubnets := ovn.remoteEndpointSubnetSet()

	toAdd := existingSubnets.Difference(newSubnets)
	toRemove := newSubnets.Difference(existingSubnets)

	return toAdd, toRemove
}

// remoteEndpointSubnetSet iterates over all known remote endpoints and subnets constructing a StringSet with
// all the remote subnets
func (ovn *SyncHandler) remoteEndpointSubnetSet() *util.StringSet {
	remoteSubnets := util.NewStringSet()

	for _, endpoint := range ovn.remoteEndpoints {
		for _, subnet := range endpoint.Spec.Subnets {
			remoteSubnets.Add(subnet)
		}
	}

	return remoteSubnets
}

// getSouthSubnetsToAddAndRemove receives the existing state for the south (our cluster) routes in the OVN
// submariner_router as an StringSet, and based on the known remote endpoints it will return the elements that need
// to be added and removed.
func (ovn *SyncHandler) getSouthSubnetsToAddAndRemove(existingSubnets *util.StringSet) ([]string, []string) {
	newSubnets := ovn.localEndpointSubnetSet()

	toAdd := existingSubnets.Difference(newSubnets)
	toRemove := newSubnets.Difference(existingSubnets)

	return toAdd, toRemove
}

// remoteEndpointSubnetSet returns an stringset with all the local subnets for this cluster based on the local endpoint
// information
func (ovn *SyncHandler) localEndpointSubnetSet() *util.StringSet {
	localSubnets := util.NewStringSet()

	if ovn.localEndpoint != nil {
		for _, subnet := range ovn.localEndpoint.Spec.Subnets {
			localSubnets.Add(subnet)
		}
	}

	return localSubnets
}
