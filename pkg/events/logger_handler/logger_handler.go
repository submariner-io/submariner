package logger_handler

import (
	k8sV1 "k8s.io/api/core/v1"
	"k8s.io/klog"

	"github.com/submariner-io/admiral/pkg/log"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/events"
)

type LoggerHandler struct {
	events.HandlerBase
}

// GetName returns the name of the event handler
func (l *LoggerHandler) GetName() string {
	return "logger"
}

// GetNetworkPlugin ... logging driver says: "all"
func (l *LoggerHandler) GetNetworkPlugin() string {
	return events.AnyNetworkPlugin
}

// TransitionToNonGateway is called when there is a transition from GatewayNode to NonGateway on the
// This will be called only when the transition happens and not periodically.
func (l *LoggerHandler) TransitionToNonGateway() error {
	klog.V(log.DEBUG).Info("The current node stopped being Gateway")
	return nil
}

// TransitionToGateway is called when there is a transition from NonGatewayNode to GatewayNode.
// This will be called only when the transition happens and not periodically.
func (l *LoggerHandler) TransitionToGateway() error {
	klog.V(log.DEBUG).Info("The current node became an active Gateway")
	return nil
}

// LocalEndpointCreated is called when the endpoint for the localCluster is created.
func (l *LoggerHandler) LocalEndpointCreated(endpoint submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("A new local Endpoint has been discovered for active Gateway on node %q with IP %q",
		endpoint.Spec.Hostname, endpoint.Spec.PrivateIP)
	return nil
}

// LocalEndpointUpdated is called when the local endpoint is updated.
func (l *LoggerHandler) LocalEndpointUpdated(endpoint submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("A local Endpoint has been discovered on node %q with IP %q",
		endpoint.Spec.Hostname, endpoint.Spec.PrivateIP)
	return nil
}

// LocalEndpointRemoved is called when the local endpoint is removed.
func (l *LoggerHandler) LocalEndpointRemoved(endpoint submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("A local Endpoint has been removed on node %q with IP %q",
		endpoint.Spec.Hostname, endpoint.Spec.PrivateIP)
	return nil
}

// RemoteEndpointCreated is called when an endpoint associated with a remote cluster is created.
func (l *LoggerHandler) RemoteEndpointCreated(endpoint submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("A remote Endpoint has been discovered on cluster %q node %q with IP %q",
		endpoint.Spec.ClusterID, endpoint.Spec.Hostname, endpoint.Spec.PrivateIP)
	return nil
}

// RemoteEndpointUpdated is called when an endpoint associated with a remote cluster is updated.
func (l *LoggerHandler) RemoteEndpointUpdated(endpoint submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("A remote Endpoint has been updated on cluster %q node %q with IP %q",
		endpoint.Spec.ClusterID, endpoint.Spec.Hostname, endpoint.Spec.PrivateIP)
	return nil
}

// RemoteEndpointRemoved is called when an endpoint associated with a remote cluster is removed
func (l *LoggerHandler) RemoteEndpointRemoved(endpoint submV1.Endpoint) error {
	klog.V(log.DEBUG).Infof("A remote Endpoint has been deleted on cluster %q node %q with IP %q",
		endpoint.Spec.ClusterID, endpoint.Spec.Hostname, endpoint.Spec.PrivateIP)
	return nil
}

// NodeAdded indicates when a node has been added to the cluster
func (l *LoggerHandler) NodeAdded(node k8sV1.Node) error {
	klog.V(log.DEBUG).Infof("A Node with name %q and addresses %#v has been added to the cluster",
		node.Name, node.Status.Addresses)
	return nil
}

// NodeUpdated indicates when a node has been updated in the cluster
func (l *LoggerHandler) NodeUpdated(node k8sV1.Node) error {
	klog.V(log.DEBUG).Infof("A Node with name %q and addresses %#v has been updated",
		node.Name, node.Status.Addresses)
	return nil
}

// NodeRemoved indicates when a node has been removed from the cluster
func (l *LoggerHandler) NodeRemoved(node k8sV1.Node) error {
	klog.V(log.DEBUG).Infof("A Node with name %q has been removed",
		node.Name)
	return nil
}
