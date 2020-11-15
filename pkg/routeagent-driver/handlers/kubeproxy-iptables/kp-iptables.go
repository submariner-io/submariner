package kp_iptables

import (
	"k8s.io/klog"

	"github.com/submariner-io/admiral/pkg/log"
	k8sV1 "k8s.io/api/core/v1"

	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
)

type SyncHandler struct {
	event.HandlerBase
	clusterID        string
	localClusterCidr []string
	localServiceCidr []string

	cniIface *cniInterface
}

func NewSyncHandler() *SyncHandler {
	return &SyncHandler{}
}

func (kp *SyncHandler) GetName() string {
	return "kubeproxy-iptables-handler"
}

func (kp *SyncHandler) GetNetworkPlugin() string {
	return event.AnyNetworkPlugin
}

func (kp *SyncHandler) Init() error {
	return nil
}

func (kp *SyncHandler) TransitionToNonGateway() error {
	klog.V(log.DEBUG).Info("The current node is no longer a Gateway")
	return nil
}

func (kp *SyncHandler) TransitionToGateway() error {
	klog.V(log.DEBUG).Info("The current node has become a Gateway")
	return nil
}

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

func (kp *SyncHandler) NodeCreated(node *k8sV1.Node) error {
	klog.V(log.DEBUG).Infof("A Node with name %q and addresses %#v has been added to the cluster",
		node.Name, node.Status.Addresses)
	// TODO: Here we have to annotate node with cni-iface ip
	return nil
}

func (kp *SyncHandler) NodeUpdated(node *k8sV1.Node) error {
	klog.V(log.DEBUG).Infof("A Node with name %q and addresses %#v has been updated",
		node.Name, node.Status.Addresses)
	return nil
}

func (kp *SyncHandler) NodeRemoved(node *k8sV1.Node) error {
	klog.V(log.DEBUG).Infof("A Node with name %q has been removed",
		node.Name)
	return nil
}
