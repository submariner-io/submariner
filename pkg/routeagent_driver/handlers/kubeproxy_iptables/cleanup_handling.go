package kubeproxy_iptables

import (
	"github.com/submariner-io/admiral/pkg/log"

	"github.com/submariner-io/submariner/pkg/routeagent_driver/cleanup"

	"k8s.io/klog"
)

func (kp *SyncHandler) installCleanupHandlers(handlers []cleanup.Handler) {
	kp.cleanupHandlers = append(kp.cleanupHandlers, handlers...)
}

func (kp *SyncHandler) nonGatewayCleanups() {
	klog.V(log.DEBUG).Infof("Handling nonGatewayCleanups")

	for _, handler := range kp.cleanupHandlers {
		if err := handler.NonGatewayCleanup(); err != nil {
			klog.Errorf("Error handling NonGatewayCleanup in %q, %s",
				handler.GetName(), err)
		}
	}
}

func (kp *SyncHandler) gatewayToNonGatewayTransitionCleanups() {
	klog.V(log.DEBUG).Infof("Handling gatewayToNonGatewayTransitionCleanups: wasGatewayPreviously %t ",
		kp.wasGatewayPreviously)

	if kp.wasGatewayPreviously {
		kp.wasGatewayPreviously = false
		for _, handler := range kp.cleanupHandlers {
			if err := handler.GatewayToNonGatewayTransition(); err != nil {
				klog.Errorf("Error handling GatewayToNonGateway cleanup in %q, %s", handler.GetName(), err)
			}
		}
	}
}
