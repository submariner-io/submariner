package kp_iptables

import (
	"k8s.io/klog"

	"github.com/submariner-io/admiral/pkg/log"
)

func (kp *SyncHandler) TransitionToNonGateway() error {
	klog.V(log.DEBUG).Info("The current node is no longer a Gateway")
	kp.syncHandlerMutex.Lock()
	defer kp.syncHandlerMutex.Unlock()
	kp.isGatewayNode = false
	kp.updateRoutingRulesForHostNetworkSupport(nil, FlushRouteTable)

	return nil
}

func (kp *SyncHandler) TransitionToGateway() error {
	klog.V(log.DEBUG).Info("The current node has become a Gateway")
	kp.syncHandlerMutex.Lock()
	defer kp.syncHandlerMutex.Unlock()
	kp.isGatewayNode = true
	// Add routes to the new endpoint on the GatewayNode.
	kp.updateRoutingRulesForHostNetworkSupport(kp.remoteSubnets.Elements(), AddRoute)

	return nil
}

func (kp *SyncHandler) populateRemoteVtepIps(vtepIP string) {
	if !kp.remoteVTEPs.Contains(vtepIP) {
		kp.remoteVTEPs.Add(vtepIP)
	}
}
