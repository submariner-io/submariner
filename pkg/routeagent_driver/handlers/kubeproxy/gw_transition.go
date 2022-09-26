/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

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

package kubeproxy

import (
	"github.com/submariner-io/admiral/pkg/log"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
)

func (kp *SyncHandler) TransitionToNonGateway() error {
	logger.V(log.DEBUG).Info("The current node is no longer a Gateway")
	kp.syncHandlerMutex.Lock()
	defer kp.syncHandlerMutex.Unlock()
	kp.isGatewayNode = false

	kp.cleanVxSubmarinerRoutes()
	// If the active Gateway transitions to a new node, we flush the HostNetwork routing table.
	kp.updateRoutingRulesForHostNetworkSupport(nil, Flush)

	err := kp.netLink.RuleDelIfPresent(netlinkAPI.NewTableRule(constants.RouteAgentHostNetworkTableID))
	if err != nil {
		logger.Errorf(err, "Unable to delete ip rule to table %d on non-Gateway node %s",
			constants.RouteAgentHostNetworkTableID, kp.hostname)
	}

	return nil
}

func (kp *SyncHandler) TransitionToGateway() error {
	logger.V(log.DEBUG).Info("The current node has become a Gateway")
	kp.cleanVxSubmarinerRoutes()

	kp.syncHandlerMutex.Lock()
	defer kp.syncHandlerMutex.Unlock()
	kp.isGatewayNode = true
	kp.wasGatewayPreviously = true

	logger.Infof("Creating the vxlan interface: %s on the gateway node", VxLANIface)

	err := kp.createVxLANInterface(kp.hostname, VxInterfaceGateway, nil)
	if err != nil {
		logger.Fatalf("Unable to create VxLAN interface on gateway node (%s): %v", kp.hostname, err)
	}

	err = kp.netLink.RuleAddIfNotPresent(netlinkAPI.NewTableRule(constants.RouteAgentHostNetworkTableID))
	if err != nil {
		logger.Errorf(err, "Unable to add ip rule to table %d on Gateway node %s",
			constants.RouteAgentHostNetworkTableID, kp.hostname)
	}

	// Add routes to the new endpoint on the GatewayNode.
	kp.updateRoutingRulesForHostNetworkSupport(kp.remoteSubnets.Elements(), Add)

	return nil
}
