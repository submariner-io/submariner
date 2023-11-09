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

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/util"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/event"
	nodeutil "github.com/submariner-io/submariner/pkg/node"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
)

type NonGatewayRouteHandler struct {
	event.HandlerBase
	smClient        submarinerClientset.Interface
	remoteEndpoints map[string]*submarinerv1.Endpoint
	k8sClientSet    clientset.Interface
	isGateway       bool
	transitSwitchIP string
}

func NewNonGatewayRouteHandler(smClientSet submarinerClientset.Interface, k8sClientset *clientset.Clientset) *NonGatewayRouteHandler {
	return &NonGatewayRouteHandler{
		smClient:        smClientSet,
		remoteEndpoints: map[string]*submarinerv1.Endpoint{},
		k8sClientSet:    k8sClientset,
	}
}

func (h *NonGatewayRouteHandler) Init() error {
	logger.Info("Starting NonGatewayRouteHandler")

	node, err := nodeutil.GetLocalNode(h.k8sClientSet)
	if err != nil {
		return errors.Wrap(err, "error getting the g/w node")
	}

	annotations := node.GetAnnotations()

	// TODO transitSwitchIP changes support needs to be added.
	transitSwitchIP, ok := annotations[constants.OvnTransitSwitchIPAnnotation]
	if !ok {
		logger.Infof("No transit switch IP configured")
		return nil
	}

	h.transitSwitchIP, err = jsonToIP(transitSwitchIP)

	return errors.Wrapf(err, "error parsing the transit switch IP")
}

func (h *NonGatewayRouteHandler) GetName() string {
	return "submariner-nongw-route-handler"
}

func (h *NonGatewayRouteHandler) GetNetworkPlugins() []string {
	return []string{cni.OVNKubernetes}
}

func (h *NonGatewayRouteHandler) RemoteEndpointCreated(endpoint *submarinerv1.Endpoint) error {
	h.remoteEndpoints[endpoint.Name] = endpoint

	if !h.isGateway || h.transitSwitchIP == "" {
		return nil
	}

	_, err := h.smClient.SubmarinerV1().
		NonGatewayRoutes(endpoint.Namespace).Create(context.TODO(),
		h.newNonGatewayRoute(endpoint), metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return errors.Wrapf(err, "error processing the remote endpoint create event for %q", endpoint.Name)
	}

	return nil
}

func (h *NonGatewayRouteHandler) RemoteEndpointRemoved(endpoint *submarinerv1.Endpoint) error {
	delete(h.remoteEndpoints, endpoint.Name)

	if !h.isGateway || h.transitSwitchIP == "" {
		return nil
	}

	if err := h.smClient.SubmarinerV1().NonGatewayRoutes(endpoint.Namespace).Delete(context.TODO(),
		endpoint.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return errors.Wrapf(err, "error deleting nonGatewayRoute %q", endpoint.Name)
	}

	return nil
}

func (h *NonGatewayRouteHandler) TransitionToNonGateway() error {
	h.isGateway = false

	return nil
}

func (h *NonGatewayRouteHandler) TransitionToGateway() error {
	h.isGateway = true

	if h.transitSwitchIP == "" {
		return nil
	}

	for _, endpoint := range h.remoteEndpoints {
		ngwr := h.newNonGatewayRoute(endpoint)

		result, err := util.CreateOrUpdate(context.TODO(), h.nonGatewayResourceInterface(endpoint.Namespace),
			ngwr, util.Replace(ngwr))
		if err != nil {
			return errors.Wrapf(err, "error creating/updating NonGatewayRoute")
		}

		logger.V(log.TRACE).Infof("NonGatewayRoute %s: %#v", result, ngwr)
	}

	return nil
}

func (h *NonGatewayRouteHandler) newNonGatewayRoute(endpoint *submarinerv1.Endpoint) *submarinerv1.NonGatewayRoute {
	return &submarinerv1.NonGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      endpoint.Name,
			Namespace: endpoint.Namespace,
		},
		RoutePolicySpec: submarinerv1.RoutePolicySpec{
			RemoteCIDRs: endpoint.Spec.Subnets,
			NextHops:    []string{h.transitSwitchIP},
		},
	}
}

func (h *NonGatewayRouteHandler) nonGatewayResourceInterface(namespace string) resource.Interface[*submarinerv1.NonGatewayRoute] {
	return &resource.InterfaceFuncs[*submarinerv1.NonGatewayRoute]{
		GetFunc:    h.smClient.SubmarinerV1().NonGatewayRoutes(namespace).Get,
		CreateFunc: h.smClient.SubmarinerV1().NonGatewayRoutes(namespace).Create,
		UpdateFunc: h.smClient.SubmarinerV1().NonGatewayRoutes(namespace).Update,
		DeleteFunc: h.smClient.SubmarinerV1().NonGatewayRoutes(namespace).Delete,
	}
}
