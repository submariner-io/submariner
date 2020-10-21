package cleanup

import (
	"os/exec"
	"strconv"

	"github.com/submariner-io/submariner/pkg/routeagent/cleanup"
)

type RoutingTableCleanupHandler struct {
	routingTable int
}

func NewRoutingTableCleanup(routingTable int) cleanup.Handler {
	return &RoutingTableCleanupHandler{routingTable: routingTable}
}

func (rt *RoutingTableCleanupHandler) GetName() string {
	return "Routing table cleanup handler"
}

func (rt *RoutingTableCleanupHandler) NonGatewayCleanup() error {
	tableStr := strconv.Itoa(rt.routingTable)
	cmd := exec.Command("/sbin/ip", "r", "flush", "table", tableStr)
	_ = cmd.Run()
	// We can safely ignore run errors error, as this table
	// won't exist in most nodes (only gateway nodes)
	return nil
}

func (rt *RoutingTableCleanupHandler) GatewayToNonGatewayTransition() error {
	return nil
}
