package kp_iptables

import (
	"k8s.io/klog"

	"github.com/submariner-io/admiral/pkg/log"
	k8sV1 "k8s.io/api/core/v1"
)

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
