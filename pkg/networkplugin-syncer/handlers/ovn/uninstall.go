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

package ovn

import (
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/libovsdbops"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/nbdb"
)

func (ovn *SyncHandler) Stop(uninstall bool) error {
	if !uninstall {
		return nil
	}

	logger.Infof("Uninstalling OVN components")

	// Delete the submariner logical router, ports and flows
	staleLRSRPred := func(item *nbdb.LogicalRouterStaticRoute) bool {
		return item.OutputPort != nil && *item.OutputPort == submarinerUpstreamRPort
	}

	err := libovsdbops.DeleteLogicalRouterStaticRoutesWithPredicate(ovn.nbdb, submarinerLogicalRouter, staleLRSRPred)
	if err != nil {
		logger.Errorf(err, "Failed to delete ovn static routes for port %q", submarinerUpstreamRPort)
	}

	subLogicalRouter := nbdb.LogicalRouter{
		Name: submarinerLogicalRouter,
	}

	subRouterToJoinLrp := nbdb.LogicalRouterPort{
		Name:     submarinerDownstreamRPort,
		MAC:      submarinerDownstreamMAC,
		Networks: []string{submarinerDownstreamNET},
	}

	err = libovsdbops.DeleteLogicalRouterPorts(ovn.nbdb, &subLogicalRouter, &subRouterToJoinLrp)
	if err != nil {
		logger.Errorf(err, "Failed to delete router ports from submariner logical router")
	}

	err = libovsdbops.DeleteLogicalRouter(ovn.nbdb, &subLogicalRouter)
	if err != nil {
		logger.Errorf(err, "Failed to delete submariner logical router")
	}

	// Delete the logical router ports and policies
	lrpStalePredicate := func(item *nbdb.LogicalRouterPolicy) bool {
		return item.Priority == ovnRoutePoliciesPrio
	}

	err = libovsdbops.DeleteLogicalRouterPoliciesWithPredicate(ovn.nbdb, ovnClusterRouter, lrpStalePredicate)
	if err != nil {
		logger.Errorf(err, "Failed to delete submariner logical route policies")
	}

	ovnLogicalRouter := nbdb.LogicalRouter{
		Name: ovnClusterRouter,
	}

	ovnRouterToJoinLrp := nbdb.LogicalRouterPort{
		Name:     ovnClusterSubmarinerRPort,
		MAC:      ovnClusterSubmarinerMAC,
		Networks: []string{ovnClusterSubmarinerNET},
	}

	err = libovsdbops.DeleteLogicalRouterPorts(ovn.nbdb, &ovnLogicalRouter, &ovnRouterToJoinLrp)
	if err != nil {
		logger.Errorf(err, "Failed to delete ports from ovn logical router")
	}

	// Delete submariner upstream switch and ports
	subGatewaySwitch := nbdb.LogicalSwitch{
		Name: submarinerUpstreamSwitch,
	}

	subGatewayToLocalNetLsp := nbdb.LogicalSwitchPort{
		Name:      submarinerUpstreamLocalnetPort,
		Type:      "localnet",
		Addresses: []string{"unknown"},
		Options: map[string]string{
			"network_name": SubmarinerUpstreamLocalnet,
		},
	}

	_, err = libovsdbops.DeleteLogicalSwitchPortsOps(ovn.nbdb, nil, &subGatewaySwitch, &subGatewayToLocalNetLsp)
	if err != nil {
		logger.Errorf(err, "Failed to to delete logical gateway ports from submariner upstream switch")
	}

	err = libovsdbops.DeleteLogicalSwitch(ovn.nbdb, submarinerUpstreamSwitch)
	if err != nil {
		logger.Errorf(err, "Failed to to delete submariner upstream switch")
	}

	subGatewayToSubRouterLsp := nbdb.LogicalSwitchPort{
		Name: submarinerUpstreamSwPort,
		Type: "router",
		Options: map[string]string{
			"router-port": submarinerUpstreamRPort,
		},
		Addresses: []string{"router"},
	}

	_, err = libovsdbops.DeleteLogicalSwitchPortsOps(ovn.nbdb, nil, &subGatewaySwitch, &subGatewayToSubRouterLsp)
	if err != nil {
		logger.Errorf(err, "Failed to delete submarinerUpstreamRPort from submariner upstream switch")
	}

	// Delete submariner downstream switch and ports
	subJoinSwitch := nbdb.LogicalSwitch{
		Name: submarinerDownstreamSwitch,
	}

	subJoinToSubRouterLsp := nbdb.LogicalSwitchPort{
		Name: submarinerDownstreamSwPort,
		Type: "router",
		Options: map[string]string{
			"router-port": submarinerDownstreamRPort,
		},
		Addresses: []string{"router"},
	}

	subJointoOvnRouterLsp := nbdb.LogicalSwitchPort{
		Name: ovnClusterSubmarinerSwPort,
		Type: "router",
		Options: map[string]string{
			"router-port": ovnClusterSubmarinerRPort,
		},
		Addresses: []string{"router"},
	}

	_, err = libovsdbops.DeleteLogicalSwitchPortsOps(ovn.nbdb, nil, &subJoinSwitch,
		[]*nbdb.LogicalSwitchPort{&subJoinToSubRouterLsp, &subJointoOvnRouterLsp}...)
	if err != nil {
		logger.Errorf(err, "Failed to delete ovnClusterSubmarinerSwPort from submariner downstream switch")
	}

	err = libovsdbops.DeleteLogicalSwitch(ovn.nbdb, submarinerDownstreamSwitch)
	if err != nil {
		logger.Errorf(err, "Failed to delete submariner downstream switch")
	}

	return nil
}
