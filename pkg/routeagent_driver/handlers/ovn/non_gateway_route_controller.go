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
	nodeutil "github.com/submariner-io/submariner/pkg/node"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	clientset "k8s.io/client-go/kubernetes"
)

type NonGatewayRouteController struct {
	nonGatewayRouteWatcher watcher.Interface
	connectionHandler      *ConnectionHandler
	remoteSubnets          sets.Set[string]
	stopCh                 chan struct{}
	transitSwitchIP        string
}

//nolint:gocritic // Ignore hugeParam
func NewNonGatewayRouteController(config watcher.Config, connectionHandler *ConnectionHandler,
	k8sClientSet clientset.Interface, namespace string,
) (*NonGatewayRouteController, error) {
	// We'll panic if config is nil, this is intentional
	var err error

	controller := &NonGatewayRouteController{
		connectionHandler: connectionHandler,
		remoteSubnets:     sets.New[string](),
		stopCh:            make(chan struct{}),
	}

	config.ResourceConfigs = []watcher.ResourceConfig{
		{
			Name:         "NonGatewayRoute watcher",
			ResourceType: &submarinerv1.NonGatewayRoute{},
			Handler: watcher.EventHandlerFuncs{
				OnCreateFunc: controller.nonGatewayRouteCreatedOrUpdated,
				OnUpdateFunc: controller.nonGatewayRouteCreatedOrUpdated,
				OnDeleteFunc: controller.nonGatewayRouteDeleted,
			},
			SourceNamespace: namespace,
		},
	}

	node, err := nodeutil.GetLocalNode(k8sClientSet)
	if err != nil {
		return nil, errors.Wrap(err, "error getting the local node info")
	}

	annotations := node.GetAnnotations()

	transitSwitchIP := annotations["k8s.ovn.org/node-transit-switch-port-ifaddr"]
	if transitSwitchIP == "" {
		// This is a non-IC setup , so this controller will not be started.
		logger.Infof("No transit switch IP configured on node %q", node.Name)
		return controller, nil
	}

	controller.transitSwitchIP, err = jsonToIP(transitSwitchIP)
	if err != nil {
		return nil, errors.Wrapf(err, "error parsing transit switch IP")
	}

	controller.nonGatewayRouteWatcher, err = watcher.New(&config)

	if err != nil {
		return nil, errors.Wrap(err, "error creating resource watcher")
	}

	err = controller.nonGatewayRouteWatcher.Start(controller.stopCh)
	if err != nil {
		return nil, errors.Wrapf(err, "error starting non gateway route controller")
	}

	logger.Info("Started NonGatewayRouteController")

	return controller, nil
}

func (g *NonGatewayRouteController) nonGatewayRouteCreatedOrUpdated(obj runtime.Object, _ int) bool {
	submNonGWRoute := obj.(*submarinerv1.NonGatewayRoute)

	err := g.reconcileRemoteSubnets(submNonGWRoute, true)
	if err != nil {
		logger.Errorf(err, "Error creating or updating router policies for remote subnets %q", g.remoteSubnets)
		return true
	}

	return false
}

func (g *NonGatewayRouteController) nonGatewayRouteDeleted(obj runtime.Object, _ int) bool {
	submNonGWRoute := obj.(*submarinerv1.NonGatewayRoute)

	err := g.reconcileRemoteSubnets(submNonGWRoute, false)
	if err != nil {
		logger.Errorf(err, "Error deleting policies for remote subnets %q", g.remoteSubnets)
		return true
	}

	return false
}

func (g *NonGatewayRouteController) reconcileRemoteSubnets(submNonGWRoute *submarinerv1.NonGatewayRoute, addSubnet bool) error {
	if len(submNonGWRoute.RoutePolicySpec.NextHops) == 0 {
		// This happens only when the RoutePolicySpec is not created correctly and added to prevent an invalid memory
		// access.
		logger.Warningf("The NonGatewayRoute does not have next hop %v", submNonGWRoute)
		return nil
	}

	// If this node belongs to same zone as gateway node, ignore the event.
	if submNonGWRoute.RoutePolicySpec.NextHops[0] != g.transitSwitchIP {
		for _, subnet := range submNonGWRoute.RoutePolicySpec.RemoteCIDRs {
			if addSubnet {
				g.remoteSubnets.Insert(subnet)
			} else {
				g.remoteSubnets.Delete(subnet)
			}
		}

		return g.connectionHandler.reconcileSubOvnLogicalRouterPolicies(g.remoteSubnets, submNonGWRoute.RoutePolicySpec.NextHops[0])
	}

	return nil
}

func (g *NonGatewayRouteController) stop() {
	if g.transitSwitchIP != "" {
		close(g.stopCh)
	}
}
