package kp_iptables

import (
	"k8s.io/klog"

	"github.com/submariner-io/admiral/pkg/log"
)

func (kp *SyncHandler) TransitionToNonGateway() error {
	klog.V(log.DEBUG).Info("The current node is no longer a Gateway")
	return nil
}

func (kp *SyncHandler) TransitionToGateway() error {
	klog.V(log.DEBUG).Info("The current node has become a Gateway")
	return nil
}
