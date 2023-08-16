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
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
)

type GatewayRouteController struct {
	gatewayRouteWatcher watcher.Interface
	connectionHandler   *ConnectionHandler
	remoteSubnets       sets.Set[string]
	stopCh              chan struct{}
	mgmtIP              string
}

//nolint:gocritic // Ignore hugeParam
func NewGatewayRouteController(config watcher.Config, connectionHandler *ConnectionHandler,
	namespace string,
) (*GatewayRouteController, error) {
	var err error

	controller := &GatewayRouteController{
		connectionHandler: connectionHandler,
		remoteSubnets:     sets.New[string](),
	}

	config.ResourceConfigs = []watcher.ResourceConfig{
		{
			Name:         "GatewayRoute watcher",
			ResourceType: &submarinerv1.GatewayRoute{},
			Handler: watcher.EventHandlerFuncs{
				OnCreateFunc: controller.gatewayRouteCreatedOrUpdated,
				OnUpdateFunc: controller.gatewayRouteCreatedOrUpdated,
				OnDeleteFunc: controller.gatewayRouteDeleted,
			},
			SourceNamespace: namespace,
		},
	}

	controller.gatewayRouteWatcher, err = watcher.New(&config)

	if err != nil {
		return nil, errors.Wrap(err, "error creating resource watcher")
	}

	mgmtIP, err := getNextHopOnK8sMgmtIntf()
	if err != nil {
		return nil, err
	}

	controller.mgmtIP = mgmtIP

	err = controller.gatewayRouteWatcher.Start(controller.stopCh)
	if err != nil {
		return nil, errors.Wrapf(err, "error starting the resource watcher")
	}

	logger.Info("Started GatewayRouteController")

	return controller, nil
}

func (g *GatewayRouteController) gatewayRouteCreatedOrUpdated(obj runtime.Object, _ int) bool {
	subMGWRoute := obj.(*submarinerv1.GatewayRoute)

	err := g.reconcileRemoteSubnets(subMGWRoute, true)
	if err != nil {
		logger.Errorf(err, "Error creating or updating router policies and static routes for remote subnets %q", g.remoteSubnets)
		return true
	}

	return false
}

func (g *GatewayRouteController) gatewayRouteDeleted(obj runtime.Object, _ int) bool {
	subMGWRoute := obj.(*submarinerv1.GatewayRoute)

	err := g.reconcileRemoteSubnets(subMGWRoute, false)
	if err != nil {
		logger.Errorf(err, "Error deleting router policies and static routes for remote subnet %q", g.remoteSubnets)
		return true
	}

	return false
}

func (g *GatewayRouteController) reconcileRemoteSubnets(subMGWRoute *submarinerv1.GatewayRoute, addSubnet bool) error {
	if len(subMGWRoute.RoutePolicySpec.NextHops) == 0 {
		// This happens only when the RoutePolicySpec is not created correctly and added to prevent an invalid memory
		// access.
		logger.Warningf("The GatewayRoute does not have next hop %v", subMGWRoute)
		return nil
	}

	if subMGWRoute.RoutePolicySpec.NextHops[0] != g.mgmtIP {
		// The current node is not the gateway node and hence ignore the event
		return nil
	}

	for _, subnet := range subMGWRoute.RoutePolicySpec.RemoteCIDRs {
		if addSubnet {
			g.remoteSubnets.Insert(subnet)
		} else {
			g.remoteSubnets.Delete(subnet)
		}
	}

	err := g.connectionHandler.reconcileSubOvnLogicalRouterPolicies(g.remoteSubnets, g.mgmtIP)
	if err != nil {
		return err
	}

	return g.connectionHandler.reconcileOvnLogicalRouterStaticRoutes(g.remoteSubnets, g.mgmtIP)
}

func (g *GatewayRouteController) stop() {
	close(g.stopCh)
}
