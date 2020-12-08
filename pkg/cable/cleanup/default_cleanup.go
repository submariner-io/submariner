package cleanup

import (
	"github.com/submariner-io/submariner/pkg/cable/strongswan"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/cleanup"
)

// TODO(mangelajo) This is a generic GetCleanupHandlers as a first step, on a later step
//                 we should remove this and use the GetCleanupHandlers from the specific
//                 cable driver(s) in use

func GetCleanupHandlers() []cleanup.Handler {
	return []cleanup.Handler{
		NewRoutingTableCleanup(strongswan.DefaultRoutingTable),
		NewXFRMCleanupHandler(),
	}
}
