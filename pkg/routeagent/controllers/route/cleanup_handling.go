package route

import (
	"github.com/submariner-io/submariner/pkg/routeagent/cleanup"

	"k8s.io/klog"
)

func (r *Controller) installCleanupHandlers(handlers []cleanup.Handler) {
	r.cleanupHandlers = append(r.cleanupHandlers, handlers...)
}

func (r *Controller) nonGatewayCleanups() {
	for _, handler := range r.cleanupHandlers {
		if err := handler.NonGatewayCleanup(); err != nil {
			klog.Errorf("Error handling NonGatewayCleanup in %q, %s",
				handler.GetName(), err)
		}
	}
}

func (r *Controller) gatewayToNonGatewayTransitionCleanups() {
	if r.wasGatewayPreviously {
		r.wasGatewayPreviously = false
		for _, handler := range r.cleanupHandlers {
			if err := handler.GatewayToNonGatewayTransition(); err != nil {
				klog.Errorf("Error handling GatewayToNonGateway cleanup in %q, %s",
					handler.GetName(), err)
			}
		}
	}
}
