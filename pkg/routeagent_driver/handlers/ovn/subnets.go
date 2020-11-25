package ovn

import "github.com/submariner-io/admiral/pkg/stringset"

func (ovn *Handler) getRemoteSubnets() stringset.Interface {
	endpointSubnets := stringset.New()

	for _, endpoint := range ovn.remoteEndpoints {
		for _, subnet := range endpoint.Spec.Subnets {
			endpointSubnets.Add(subnet)
		}
	}

	return endpointSubnets
}
