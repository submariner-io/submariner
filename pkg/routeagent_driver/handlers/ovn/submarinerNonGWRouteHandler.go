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
	"context"
	"sync"

	"github.com/pkg/errors"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/event"
	nodeutil "github.com/submariner-io/submariner/pkg/node"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

type NonGatewayRouteHandler struct {
	event.HandlerBase
	mutex           sync.Mutex
	smClient        submarinerClientset.Interface
	localEndpoint   *submarinerv1.Endpoint
	remoteEndpoints map[string]*submarinerv1.Endpoint
	k8sClientSet    clientset.Interface
	isGateway       bool
	transitSwitchIP string
}

func NewNonGatewayRouteHandler(smClientSet submarinerClientset.Interface, k8sClientset *clientset.Clientset) *NonGatewayRouteHandler {
	// We'll panic if env is nil, this is intentional
	return &NonGatewayRouteHandler{
		smClient:        smClientSet,
		remoteEndpoints: map[string]*submarinerv1.Endpoint{},
		k8sClientSet:    k8sClientset,
	}
}

func (nonGatewayRouteHandler *NonGatewayRouteHandler) Init() error {
	logger.Infof("Starting NonGatewayRouteHandler")

	node, err := nodeutil.GetLocalNode(nonGatewayRouteHandler.k8sClientSet)
	if err != nil {
		return errors.Wrap(err, "error getting the g/w node")
	}

	annotations := node.GetAnnotations()

	transitSwitchIP, ok := annotations["k8s.ovn.org/node-transit-switch-port-ifaddr"]
	if !ok {
		logger.Infof("No transit switch IP configured")
		return nil
	}

	nonGatewayRouteHandler.transitSwitchIP = transitSwitchIP

	return nil
}

func (nonGatewayRouteHandler *NonGatewayRouteHandler) GetName() string {
	return "submariner-nongw-route-handler"
}

func (nonGatewayRouteHandler *NonGatewayRouteHandler) GetNetworkPlugins() []string {
	// TODO enable when we switch to new implementation
	// return []string{cni.OVNKubernetes}
	return []string{}
}

func (nonGatewayRouteHandler *NonGatewayRouteHandler) LocalEndpointCreated(endpoint *submarinerv1.Endpoint) error {
	nonGatewayRouteHandler.mutex.Lock()
	defer nonGatewayRouteHandler.mutex.Unlock()

	nonGatewayRouteHandler.localEndpoint = endpoint

	return nil
}

func (nonGatewayRouteHandler *NonGatewayRouteHandler) RemoteEndpointCreated(endpoint *submarinerv1.Endpoint) error {
	nonGatewayRouteHandler.mutex.Lock()
	defer nonGatewayRouteHandler.mutex.Unlock()

	if !nonGatewayRouteHandler.isGateway || nonGatewayRouteHandler.transitSwitchIP == "" {
		return nil
	}

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		_, err := nonGatewayRouteHandler.smClient.SubmarinerV1().
			NonGatewayRoutes(endpoint.Namespace).Create(context.TODO(),
			nonGatewayRouteHandler.getNonGatewayRoute(endpoint), metav1.CreateOptions{})
		if !apierrors.IsAlreadyExists(err) {
			return errors.Wrapf(err, "error processing the remote endpoint create event for %q", endpoint.Name)
		}
		return nil
	})

	return errors.Wrapf(retryErr, "processing the remote endpoint event failed even after retry")
}

func (nonGatewayRouteHandler *NonGatewayRouteHandler) RemoteEndpointRemoved(endpoint *submarinerv1.Endpoint) error {
	nonGatewayRouteHandler.mutex.Lock()
	defer nonGatewayRouteHandler.mutex.Unlock()

	delete(nonGatewayRouteHandler.remoteEndpoints, endpoint.Name)

	if !nonGatewayRouteHandler.isGateway || nonGatewayRouteHandler.transitSwitchIP == "" {
		return nil
	}

	if err := nonGatewayRouteHandler.smClient.SubmarinerV1().NonGatewayRoutes(endpoint.Namespace).Delete(context.TODO(),
		endpoint.Name, metav1.DeleteOptions{}); err != nil {
		return errors.Wrapf(err, "error deleting nonGatewayRoute %q", endpoint.Name)
	}

	return nil
}

func (nonGatewayRouteHandler *NonGatewayRouteHandler) TransitionToNonGateway() error {
	nonGatewayRouteHandler.mutex.Lock()
	defer nonGatewayRouteHandler.mutex.Unlock()

	nonGatewayRouteHandler.isGateway = false

	return nil
}

func (nonGatewayRouteHandler *NonGatewayRouteHandler) TransitionToGateway() error {
	nonGatewayRouteHandler.mutex.Lock()
	defer nonGatewayRouteHandler.mutex.Unlock()

	nonGatewayRouteHandler.isGateway = true
	if nonGatewayRouteHandler.transitSwitchIP != "" {
		for _, endpoint := range nonGatewayRouteHandler.remoteEndpoints {
			if _, err := nonGatewayRouteHandler.smClient.SubmarinerV1().NonGatewayRoutes(endpoint.Namespace).Update(context.TODO(),
				nonGatewayRouteHandler.getNonGatewayRoute(endpoint), metav1.UpdateOptions{}); err != nil {
				if !apierrors.IsNotFound(err) {
					_, err = nonGatewayRouteHandler.smClient.SubmarinerV1().NonGatewayRoutes(endpoint.Namespace).Create(context.TODO(),
						nonGatewayRouteHandler.getNonGatewayRoute(endpoint), metav1.CreateOptions{})

					return errors.Wrapf(err, "error deleting nonGatewayRoute %q", endpoint.Name)
				}
			}
		}
	}

	return nil
}

func (nonGatewayRouteHandler *NonGatewayRouteHandler) getNonGatewayRoute(endpoint *submarinerv1.Endpoint) *submarinerv1.NonGatewayRoute {
	return &submarinerv1.NonGatewayRoute{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NonGatewayRoute",
			APIVersion: "submariner.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      endpoint.Name,
			Namespace: endpoint.Namespace,
		},
		RoutePolicySpec: submarinerv1.RoutePolicySpec{
			RemoteCIDRs: endpoint.Spec.Subnets,
			NextHops:    []string{nonGatewayRouteHandler.transitSwitchIP},
		},
	}
}
