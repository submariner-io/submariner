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
	nodeutil "github.com/submariner-io/submariner/pkg/node"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	clientset "k8s.io/client-go/kubernetes"
)

type NonGatewayRouteController struct {
	resourceSyncer    syncer.Interface
	connectionHandler *ConnectionHandler
	mutex             sync.Mutex
	remoteSubnets     sets.Set[string]
	stopCh            chan struct{}
	transitSwitchIP   string
	k8sClientSet      clientset.Interface
}

func NewNonGatewayRoute(config *syncer.ResourceSyncerConfig, connectionHandler *ConnectionHandler,
	k8sClientSet clientset.Interface,
) (*NonGatewayRouteController, error) {
	// We'll panic if config is nil, this is intentional
	var err error

	controller := &NonGatewayRouteController{
		connectionHandler: connectionHandler,
		remoteSubnets:     sets.New[string](),
		k8sClientSet:      k8sClientSet,
	}

	federator := federate.NewUpdateStatusFederator(config.SourceClient, config.RestMapper, corev1.NamespaceAll)

	controller.resourceSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:                "NonGatewayRoute syncer",
		ResourceType:        &submarinerv1.NonGatewayRoute{},
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

	node, err := nodeutil.GetLocalNode(k8sClientSet)
	if err != nil {
		return nil, errors.Wrap(err, "error getting the node")
	}

	annotations := node.GetAnnotations()

	transitSwitchIP, ok := annotations["k8s.ovn.org/node-transit-switch-port-ifaddr"]
	if !ok || transitSwitchIP == "" {
		logger.Infof("No transit switch IP configured")
		return controller, nil
	}

	controller.transitSwitchIP, err = jsonToIP(transitSwitchIP)
	if err != nil {
		return nil, errors.Wrapf(err, "Error parsing transit switch IP")
	}

	err = controller.resourceSyncer.Start(controller.stopCh)
	if err != nil {
		return nil, errors.Wrapf(err, "error starting non gateway route controller")
	}

	logger.Infof("Started NonGatewayRouteController")

	return controller, nil
}

func (g *NonGatewayRouteController) process(from runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	submGWRoute := from.(*submarinerv1.NonGatewayRoute)
	if submGWRoute.RoutePolicySpec.NextHops[0] != g.transitSwitchIP {
		if op == syncer.Create || op == syncer.Update {
			for _, subnet := range submGWRoute.RoutePolicySpec.RemoteCIDRs {
				g.remoteSubnets.Insert(subnet)
			}
		} else if op == syncer.Delete {
			for _, subnet := range submGWRoute.RoutePolicySpec.RemoteCIDRs {
				g.remoteSubnets.Delete(subnet)
			}
		}

		err := g.connectionHandler.ReconcileSubOvnLogicalRouterPolicies(g.remoteSubnets, submGWRoute.RoutePolicySpec.NextHops[0])
		if err != nil {
			logger.Errorf(err, "error reconciling router policies for remote subnet %q", g.remoteSubnets)
			return nil, true
		}
	}

	return nil, false
}

func (g *NonGatewayRouteController) Stop() {
	if g.transitSwitchIP != "" {
		close(g.stopCh)
	}
}
