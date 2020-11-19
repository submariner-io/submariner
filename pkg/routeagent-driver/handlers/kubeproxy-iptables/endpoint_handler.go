package kp_iptables

import (
	"fmt"

	"k8s.io/klog"

	"github.com/submariner-io/admiral/pkg/log"

	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/util"
)

func (kp *SyncHandler) LocalEndpointCreated(endpoint *submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("A new Endpoint for the local cluster has been created: %#v", endpoint)
	kp.localCableDriver = endpoint.Spec.Backend

	return nil
}

func (kp *SyncHandler) LocalEndpointUpdated(endpoint *submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("The Endpoint for the local cluster has been updated: %#v", endpoint)
	return nil
}

func (kp *SyncHandler) LocalEndpointRemoved(endpoint *submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("The Endpoint for the local cluster has been removed: %#v", endpoint)
	return nil
}

func (kp *SyncHandler) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("A new Endpoint for remote cluster %q has been created: %#v",
		endpoint.Spec.ClusterID, endpoint)

	if err := kp.overlappingSubnets(endpoint.Spec.Subnets); err != nil {
		// Skip processing the endpoint when CIDRs overlap
		return err
	}

	kp.updateIptableRulesForInterclusterTraffic(endpoint.Spec.Subnets)

	return nil
}

func (kp *SyncHandler) RemoteEndpointUpdated(endpoint *submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("A new Endpoint for remote cluster %q has been updated: %#v",
		endpoint.Spec.ClusterID, endpoint)

	if err := kp.overlappingSubnets(endpoint.Spec.Subnets); err != nil {
		// Skip processing the endpoint when CIDRs overlap
		return err
	}

	return nil
}

func (kp *SyncHandler) RemoteEndpointRemoved(endpoint *submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("A new Endpoint for remote cluster %q has been removed: %#v",
		endpoint.Spec.ClusterID, endpoint)
	return nil
}

func (kp *SyncHandler) overlappingSubnets(remoteSubnets []string) error {
	// If the remoteSubnets [*] overlap with local cluster Pod/Service CIDRs we
	// should not update the IPTable rules on the host, as it will disrupt the
	// functionality of the local cluster. So, lets validate that subnets do not
	// overlap before we program any IPTable rules on the host for inter-cluster
	// traffic.
	// [*] Note: In a non-GlobalNet deployment, remoteSubnets will be a list of
	// Pod/Service CIDRs, whereas in a GlobalNet deployment, it will be a list of
	// globalCIDRs allocated to the clusters.
	for _, serviceCidr := range kp.localServiceCidr {
		overlap, err := util.IsOverlappingCIDR(remoteSubnets, serviceCidr)
		if err != nil {
			// Ideally this case will never hit, as the subnets are valid CIDRs
			klog.Warningf("unable to validate overlapping Service CIDR: %s", err)
		}

		if overlap {
			return fmt.Errorf("Local Service CIDR %q, overlaps with remote cluster %s", serviceCidr, err)
		}
	}

	for _, podCidr := range kp.localClusterCidr {
		overlap, err := util.IsOverlappingCIDR(remoteSubnets, podCidr)
		if err != nil {
			klog.Warningf("unable to validate overlapping Pod CIDR: %s", err)
		}

		if overlap {
			return fmt.Errorf("Local Pod CIDR %q, overlaps with remote cluster %s", podCidr, err)
		}
	}

	return nil
}
