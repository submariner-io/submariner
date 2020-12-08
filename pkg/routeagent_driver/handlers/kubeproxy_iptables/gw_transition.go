package kubeproxy_iptables

import (
	"k8s.io/klog"

	"github.com/submariner-io/admiral/pkg/log"

	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
)

func (kp *SyncHandler) TransitionToNonGateway() error {
	klog.V(log.DEBUG).Info("The current node is no longer a Gateway")
	kp.syncHandlerMutex.Lock()
	defer kp.syncHandlerMutex.Unlock()
	kp.isGatewayNode = false

	kp.cleanVxSubmarinerRoutes()
	// If the active Gateway transitions to a new node, we flush the HostNetwork routing table.
	kp.updateRoutingRulesForHostNetworkSupport(nil, Flush)
	kp.nonGatewayCleanups()
	kp.gatewayToNonGatewayTransitionCleanups()
	err := kp.configureIPRule(Delete)
	if err != nil {
		klog.Errorf("Unable to delete ip rule to table %d on non-Gateway node %s: %v",
			constants.RouteAgentHostNetworkTableID, kp.hostname, err)
	}

	return nil
}

func (kp *SyncHandler) TransitionToGateway() error {
	klog.V(log.DEBUG).Info("The current node has become a Gateway")
	kp.cleanVxSubmarinerRoutes()

	kp.syncHandlerMutex.Lock()
	defer kp.syncHandlerMutex.Unlock()
	kp.isGatewayNode = true
	kp.wasGatewayPreviously = true

	klog.Infof("Creating the vxlan interface: %s on the gateway node", VxLANIface)

	err := kp.createVxLANInterface(kp.hostname, VxInterfaceGateway, nil)
	if err != nil {
		klog.Fatalf("Unable to create VxLAN interface on gateway node (%s): %v", kp.hostname, err)
	}

	err = kp.configureIPRule(Add)
	if err != nil {
		klog.Errorf("Unable to add ip rule to table %d on Gateway node %s: %v",
			constants.RouteAgentHostNetworkTableID, kp.hostname, err)
	}

	// Add routes to the new endpoint on the GatewayNode.
	kp.updateRoutingRulesForHostNetworkSupport(kp.remoteSubnets.Elements(), Add)

	return nil
}
