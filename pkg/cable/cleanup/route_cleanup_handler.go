/*
© 2021 Red Hat, Inc. and others

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package cleanup

import (
	"os/exec"
	"strconv"

	"github.com/submariner-io/submariner/pkg/routeagent_driver/cleanup"
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
