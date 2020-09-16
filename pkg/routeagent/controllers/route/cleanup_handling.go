package route

import (
	"github.com/submariner-io/submariner/pkg/routeagent/cleanup"
	"k8s.io/klog"
)

func (r *Controller) installCleanupHandlers(handlers []cleanup.Handler) {
	r.cleanupHandlers = append(r.cleanupHandlers, handlers...)
}

func (r *Controller) gatewayToNonGatewayTransitionCleanups() {
	for _, handler := range r.cleanupHandlers {
		if err := handler.GatewayToNonGatewayTransition(); err != nil {
			klog.Errorf("Error handling GatewayToNonGateway cleanup in %q, %s",
				handler.GetName(), err)
		}
	}
}
