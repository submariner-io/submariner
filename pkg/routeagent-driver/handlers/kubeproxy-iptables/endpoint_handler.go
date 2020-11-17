package kp_iptables

import (
	"k8s.io/klog"

	"github.com/submariner-io/admiral/pkg/log"

	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

func (kp *SyncHandler) LocalEndpointCreated(endpoint *submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("A new Endpoint for the local cluster has been created: %#v", endpoint)
	// TODO: If subnets are overlapping, ignore that endpoint
	return nil
}

func (kp *SyncHandler) LocalEndpointUpdated(endpoint *submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("The Endpoint for the local cluster has been updated: %#v", endpoint)
	// TODO: If subnets are overlapping, ignore that endpoint
	return nil
}

func (kp *SyncHandler) LocalEndpointRemoved(endpoint *submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("The Endpoint for the local cluster has been removed: %#v", endpoint)
	return nil
}

func (kp *SyncHandler) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("A new Endpoint for remote cluster %q has been created: %#v",
		endpoint.Spec.ClusterID, endpoint)
	return nil
}

func (kp *SyncHandler) RemoteEndpointUpdated(endpoint *submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("A new Endpoint for remote cluster %q has been updated: %#v",
		endpoint.Spec.ClusterID, endpoint)
	return nil
}

func (kp *SyncHandler) RemoteEndpointRemoved(endpoint *submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("A new Endpoint for remote cluster %q has been removed: %#v",
		endpoint.Spec.ClusterID, endpoint)
	return nil
}
