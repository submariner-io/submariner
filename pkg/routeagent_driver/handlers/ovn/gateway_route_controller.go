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
	"sync"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/syncer"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
)

type GatewayRouteController struct {
	resourceSyncer    syncer.Interface
	connectionHandler *ConnectionHandler
	mutex             sync.Mutex
	remoteSubnets     sets.Set[string]
	stopCh            chan struct{}
	mgmtIP            string
}

func NewGatewayRoute(config *syncer.ResourceSyncerConfig, connectionHandler *ConnectionHandler,
) (*GatewayRouteController, error) {
	var err error

	controller := &GatewayRouteController{
		connectionHandler: connectionHandler,
		remoteSubnets:     sets.New[string](),
	}

	federator := federate.NewUpdateStatusFederator(config.SourceClient, config.RestMapper, corev1.NamespaceAll)

	controller.resourceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:                "GatewayRoute syncer",
		ResourceType:        &submarinerv1.GatewayRoute{},
		SourceClient:        config.SourceClient,
		SourceNamespace:     "submariner-operator",
		RestMapper:          config.RestMapper,
		Federator:           federator,
		Scheme:              config.Scheme,
		Transform:           controller.process,
		ResourcesEquivalent: syncer.AreSpecsEquivalent,
	})

	if err != nil {
		return nil, errors.Wrap(err, "error creating resource syncer")
	}

	mgmtIP, err := getNextHopOnK8sMgmtIntf()
	if err != nil {
		return nil, err
	}

	controller.mgmtIP = mgmtIP

	err = controller.resourceSyncer.Start(controller.stopCh)
	if err != nil {
		return nil, errors.Wrapf(err, "error starting the resource syncer")
	}

	logger.Infof("Started GatewayRouteController")

	return controller, nil
}

func (g *GatewayRouteController) process(from runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	subMGWRoute := from.(*submarinerv1.GatewayRoute)
	if subMGWRoute.RoutePolicySpec.NextHops[0] == g.mgmtIP {
		if op == syncer.Create || op == syncer.Update {
			for _, subnet := range subMGWRoute.RoutePolicySpec.RemoteCIDRs {
				g.remoteSubnets.Insert(subnet)
			}
		} else {
			for _, subnet := range subMGWRoute.RoutePolicySpec.RemoteCIDRs {
				g.remoteSubnets.Delete(subnet)
			}
		}

		err := g.connectionHandler.ReconcileSubOvnLogicalRouterPolicies(g.remoteSubnets, g.mgmtIP)
		if err != nil {
			logger.Errorf(err, "error reconciling router policies for remote subnet %q", g.remoteSubnets)
			return nil, true
		}

		err = g.connectionHandler.ReconcileOvnLogicalRouterStaticRoutes(g.remoteSubnets, g.mgmtIP)
		if err != nil {
			logger.Errorf(err, "error reconciling static routes for remote subnet %q", g.remoteSubnets)
			return nil, true
		}
	}

	return nil, false
}

func (g *GatewayRouteController) Stop() {
	close(g.stopCh)
}
