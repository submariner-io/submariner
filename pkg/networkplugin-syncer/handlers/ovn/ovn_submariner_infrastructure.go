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
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/sbdb"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"k8s.io/klog"
)

// Ensure the core submariner ovn topology is setup and current.
// Creates the following resources
//
// (localnetPort) - subGatewaySwitch
//
//		&&&
//
// subRouter 							 subJoinSwitch					 		ovnClusterRouter
//     |(subRouter2JoinLRP)-(subJoin2RouterLSP)|(subClusterSwPort)-(subJoin2RouterLSP)|
func (ovn *SyncHandler) ensureSubmarinerInfra() error {
	klog.Info("Ensuring submariner ovn topology and connecting to ovn-cluster-router")

	lbGroups, err := libovsdbops.FindLoadBalancerGroupsWithPredicate(ovn.nbdb, func(item *nbdb.LoadBalancerGroup) bool {
		return item.Name == ovnLBGroup
	})
	if err != nil {
		return errors.Wrap(err, "failed to find ovn load balancer group")
	}

	if len(lbGroups) > 1 {
		return errors.Wrap(err, "Found more than one ovn load balancer group")
	}

	ovnLogicalRouter := nbdb.LogicalRouter{
		Name: ovnClusterRouter,
	}

	subLogicalRouter := nbdb.LogicalRouter{
		Name:              submarinerLogicalRouter,
		LoadBalancerGroup: []string{lbGroups[0].UUID},
	}

	err = libovsdbops.CreateOrUpdateLogicalRouter(ovn.nbdb, &subLogicalRouter)
	if err != nil {
		return errors.Wrap(err, "Failed to Create submariner logical router")
	}

	// setup LRP to connect the submariner logical router to the submariner logical switch
	subRouterToJoinLrp := nbdb.LogicalRouterPort{
		Name:     submarinerDownstreamRPort,
		MAC:      submarinerDownstreamMAC,
		Networks: []string{submarinerDownstreamNET},
	}

	err = libovsdbops.CreateOrUpdateLogicalRouterPorts(ovn.nbdb, &subLogicalRouter, &subRouterToJoinLrp)
	if err != nil {
		return errors.Wrap(err, "Failed to create and add router ports to submariner logical router")
	}

	ovnRouterToJoinLrp := nbdb.LogicalRouterPort{
		Name:     ovnClusterSubmarinerRPort,
		MAC:      ovnClusterSubmarinerMAC,
		Networks: []string{ovnClusterSubmarinerNET},
	}

	err = libovsdbops.CreateOrUpdateLogicalRouterPorts(ovn.nbdb, &ovnLogicalRouter, &ovnRouterToJoinLrp)
	if err != nil {
		return errors.Wrap(err, "Failed to create and add router ports to submariner logical router")
	}

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

	err = libovsdbops.CreateOrUpdateLogicalSwitchPortsAndSwitch(ovn.nbdb, &subGatewaySwitch, &subGatewayToLocalNetLsp)
	if err != nil {
		return errors.Wrap(err, "Failed to Create submariner logical gateway switch and associated ports")
	}

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

	err = libovsdbops.CreateOrUpdateLogicalSwitchPortsAndSwitch(ovn.nbdb, &subJoinSwitch,
		[]*nbdb.LogicalSwitchPort{&subJoinToSubRouterLsp, &subJointoOvnRouterLsp}...)

	// At this point, we are missing the ovn_cluster_router policies and the
	// local-to-remote & remote-to-local routes in submariner_router, that
	// depends on endpoint details that we will receive via events.
	return errors.Wrap(err, "Failed to Create submariner logical join switch and associated ports")
}

// setupOvnClusterRouterLRPs configures the ovn cluster router's logical router policies.
func (ovn *SyncHandler) setupOvnClusterRouterLRPs() error {
	remoteSubnets := ovn.remoteEndpointSubnetSet()

	if remoteSubnets.Size() == 0 {
		klog.V(log.DEBUG).Info("No Remote Subnets to Process")
		return nil
	}

	klog.V(log.DEBUG).Infof("Reconciling these raw remote subnets %v to logical router policies", remoteSubnets.Elements())

	return ovn.reconcileSubOvnLogicalRouterPolicies(remoteSubnets)
}

// associateSubmarinerRouterToChassis locks the submariner_router to a specific node.
func (ovn *SyncHandler) associateSubmarinerRouterToChassis(chassis *sbdb.Chassis) error {
	subLogicalRouter := nbdb.LogicalRouter{
		Name: submarinerLogicalRouter,
	}

	submarinerRouter, err := libovsdbops.GetLogicalRouter(ovn.nbdb, &subLogicalRouter)
	if err != nil {
		return errors.Wrap(err, "Failed to fetch the ovn submariner router")
	}

	if submarinerRouter.Options == nil {
		submarinerRouter.Options = map[string]string{}
	}

	submarinerRouter.Options["chassis"] = chassis.Name

	err = libovsdbops.CreateOrUpdateLogicalRouter(ovn.nbdb, submarinerRouter)

	return errors.Wrap(err, "failed to set chassis option on ovn submariner router")
}

// createOrUpdateSubmarinerExternalPort ensures that the submariner external Port
// can communicate with the node where the gateway switch is located.
func (ovn *SyncHandler) createOrUpdateSubmarinerExternalPort() error {
	klog.Info("Ensuring connection between submariner router and submariner gateway switch")

	subGatewaySwitch := nbdb.LogicalSwitch{
		Name: submarinerUpstreamSwitch,
	}

	subGatewayToSubRouterLsp := nbdb.LogicalSwitchPort{
		Name: submarinerUpstreamSwPort,
		Type: "router",
		Options: map[string]string{
			"router-port": submarinerUpstreamRPort,
		},
		Addresses: []string{"router"},
	}

	err := libovsdbops.CreateOrUpdateLogicalSwitchPortsOnSwitch(ovn.nbdb, &subGatewaySwitch, &subGatewayToSubRouterLsp)
	if err != nil {
		return errors.Wrap(err, "failed to add gateway to submariner router LSP to the submariner gateway switch")
	}

	subLogicalRouter := nbdb.LogicalRouter{
		Name: submarinerLogicalRouter,
	}

	subRouterTosubGatewayLrp := nbdb.LogicalRouterPort{
		Name:     submarinerUpstreamRPort,
		MAC:      submarinerUpstreamMAC,
		Networks: []string{submarinerUpstreamNET},
	}

	err = libovsdbops.CreateOrUpdateLogicalRouterPorts(ovn.nbdb, &subLogicalRouter, &subRouterTosubGatewayLrp)

	return errors.Wrap(err, "failed to add gateway to submariner router LSP to the submariner gateway switch")
}

// updateSubmarinerRouterRemoteRoutes reconciles ovn static routes on the submariner router.
func (ovn *SyncHandler) updateSubmarinerRouterRemoteRoutes() error {
	remoteSubnets := ovn.remoteEndpointSubnetSet()

	klog.V(log.DEBUG).Infof("reconciling north static routes on %q router for subnets %v", submarinerLogicalRouter,
		remoteSubnets.Elements())

	return ovn.reconcileSubOvnLogicalRouterStaticRoutes(submarinerUpstreamRPort, HostUpstreamIP, remoteSubnets)
}

func (ovn *SyncHandler) updateSubmarinerRouterLocalRoutes() error {
	localSubnets := ovn.localEndpointSubnetSet()

	klog.V(log.DEBUG).Infof("reconciling south static routes on %q for subnets %v", submarinerLogicalRouter,
		localSubnets)

	return ovn.reconcileSubOvnLogicalRouterStaticRoutes(submarinerDownstreamRPort, ovnClusterSubmarinerIP, localSubnets)
}
