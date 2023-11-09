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

package event

import (
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	k8sV1 "k8s.io/api/core/v1"
)

const AnyNetworkPlugin = ""

type HandlerState interface {
	IsOnGateway() bool
	GetRemoteEndpoints() []submV1.Endpoint
}

type DefaultHandlerState struct{}

func (c *DefaultHandlerState) IsOnGateway() bool {
	return false
}

func (c *DefaultHandlerState) GetRemoteEndpoints() []submV1.Endpoint {
	return nil
}

type Handler interface {
	// Init is called once on startup to let the handler initialize any state it needs.
	Init() error

	// SetHandlerState is called once on startup after Init with the HandlerState that can be used to access global data from event callbacks.
	SetState(handlerCtx HandlerState)

	// GetName returns the name of the event handler
	GetName() string

	// GetNetworkPlugin returns the kubernetes network plugin that this handler supports.
	GetNetworkPlugins() []string

	// Stop is called once during shutdown to let the handler perform any cleanup.
	Stop() error

	// Uninstall is called once after shutdown to let the handler process a Submariner uninstallation.
	Uninstall() error

	// TransitionToNonGateway is called once for each transition of the local node from Gateway to a non-Gateway.
	TransitionToNonGateway() error

	// TransitionToGateway is called once for each transition of the local node from non-Gateway to a Gateway.
	TransitionToGateway() error

	// LocalEndpointCreated is called when an endpoint for the local cluster is created.
	LocalEndpointCreated(endpoint *submV1.Endpoint) error

	// LocalEndpointUpdated is called when an endpoint for the local cluster is updated.
	LocalEndpointUpdated(endpoint *submV1.Endpoint) error

	// LocalEndpointRemoved is called when an endpoint for the local cluster is removed.
	LocalEndpointRemoved(endpoint *submV1.Endpoint) error

	// RemoteEndpointCreated is called when an endpoint associated with a remote cluster is created.
	RemoteEndpointCreated(endpoint *submV1.Endpoint) error

	// RemoteEndpointUpdated is called when an endpoint associated with a remote cluster is updated.
	RemoteEndpointUpdated(endpoint *submV1.Endpoint) error

	// RemoteEndpointRemoved is called when an endpoint associated with a remote cluster is removed
	RemoteEndpointRemoved(endpoint *submV1.Endpoint) error

	// NodeCreated indicates when a node has been added to the cluster
	NodeCreated(node *k8sV1.Node) error

	// NodeUpdated indicates when a node has been updated in the cluster
	NodeUpdated(node *k8sV1.Node) error

	// NodeRemoved indicates when a node has been removed from the cluster
	NodeRemoved(node *k8sV1.Node) error
}

// Base structure for event handlers that stubs out methods considered to be optional.
type HandlerBase struct {
	handlerState HandlerState
}

func (ev *HandlerBase) Init() error {
	return nil
}

func (ev *HandlerBase) SetState(handlerState HandlerState) {
	ev.handlerState = handlerState
}

func (ev *HandlerBase) State() HandlerState {
	if ev.handlerState == nil {
		return &DefaultHandlerState{}
	}

	return ev.handlerState
}

func (ev *HandlerBase) Stop() error {
	return nil
}

func (ev *HandlerBase) Uninstall() error {
	return nil
}

func (ev *HandlerBase) TransitionToNonGateway() error {
	return nil
}

func (ev *HandlerBase) TransitionToGateway() error {
	return nil
}

func (ev *HandlerBase) LocalEndpointCreated(_ *submV1.Endpoint) error {
	return nil
}

func (ev *HandlerBase) LocalEndpointUpdated(_ *submV1.Endpoint) error {
	return nil
}

func (ev *HandlerBase) LocalEndpointRemoved(_ *submV1.Endpoint) error {
	return nil
}

func (ev *HandlerBase) RemoteEndpointCreated(_ *submV1.Endpoint) error {
	return nil
}

func (ev *HandlerBase) RemoteEndpointUpdated(_ *submV1.Endpoint) error {
	return nil
}

func (ev *HandlerBase) RemoteEndpointRemoved(_ *submV1.Endpoint) error {
	return nil
}

func (ev *HandlerBase) NodeCreated(_ *k8sV1.Node) error {
	return nil
}

func (ev *HandlerBase) NodeUpdated(_ *k8sV1.Node) error {
	return nil
}

func (ev *HandlerBase) NodeRemoved(_ *k8sV1.Node) error {
	return nil
}
