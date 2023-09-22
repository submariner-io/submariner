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

package testing

import (
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	v12 "k8s.io/api/core/v1"
)

type TestEvent struct {
	Name      string
	Handler   string
	Parameter interface{}
}

type TestHandler struct {
	event.HandlerBase
	Name          string
	NetworkPlugin string
	FailOnEvent   error
	Events        chan TestEvent
	Initialized   bool
}

func NewTestHandler(name, networkPlugin string, events chan TestEvent) *TestHandler {
	return &TestHandler{
		Name:          name,
		NetworkPlugin: networkPlugin,
		Events:        events,
		Initialized:   false,
	}
}

func (t *TestHandler) addEvent(eventName string, param interface{}) error {
	ev := TestEvent{
		Name:      eventName,
		Parameter: param,
		Handler:   t.Name,
	}

	t.Events <- ev

	return t.FailOnEvent
}

func (t *TestHandler) Init() error {
	t.Initialized = true

	return nil
}

func (t *TestHandler) GetName() string {
	return t.Name
}

func (t *TestHandler) GetNetworkPlugins() []string {
	return []string{t.NetworkPlugin}
}

const (
	EvTransitionToNonGateway = "TransitionToNonGateway"
	EvTransitionToGateway    = "TransitionToGateway"
	EvLocalEndpointCreated   = "LocalEndpointCreated"
	EvLocalEndpointUpdated   = "LocalEndpointUpdated"
	EvLocalEndpointRemoved   = "LocalEndpointRemoved"
	EvRemoteEndpointCreated  = "RemoteEndpointCreated"
	EvRemoteEndpointUpdated  = "RemoteEndpointUpdated"
	EvRemoteEndpointRemoved  = "RemoteEndpointRemoved"
	EvNodeCreated            = "NodeCreated"
	EvNodeUpdated            = "NodeUpdated"
	EvNodeRemoved            = "NodeRemoved"
	EvStop                   = "Stop"
	EvUninstall              = "Uninstall"
)

func (t *TestHandler) Stop() error {
	return t.addEvent(EvStop, nil)
}

func (t *TestHandler) Uninstall() error {
	return t.addEvent(EvUninstall, nil)
}

func (t *TestHandler) TransitionToNonGateway() error {
	return t.addEvent(EvTransitionToNonGateway, nil)
}

func (t *TestHandler) TransitionToGateway() error {
	return t.addEvent(EvTransitionToGateway, nil)
}

func (t *TestHandler) LocalEndpointCreated(endpoint *v1.Endpoint) error {
	return t.addEvent(EvLocalEndpointCreated, endpoint)
}

func (t *TestHandler) LocalEndpointUpdated(endpoint *v1.Endpoint) error {
	return t.addEvent(EvLocalEndpointUpdated, endpoint)
}

func (t *TestHandler) LocalEndpointRemoved(endpoint *v1.Endpoint) error {
	return t.addEvent(EvLocalEndpointRemoved, endpoint)
}

func (t *TestHandler) RemoteEndpointCreated(endpoint *v1.Endpoint) error {
	return t.addEvent(EvRemoteEndpointCreated, endpoint)
}

func (t *TestHandler) RemoteEndpointUpdated(endpoint *v1.Endpoint) error {
	return t.addEvent(EvRemoteEndpointUpdated, endpoint)
}

func (t *TestHandler) RemoteEndpointRemoved(endpoint *v1.Endpoint) error {
	return t.addEvent(EvRemoteEndpointRemoved, endpoint)
}

func (t *TestHandler) NodeCreated(node *v12.Node) error {
	return t.addEvent(EvNodeCreated, node)
}

func (t *TestHandler) NodeUpdated(node *v12.Node) error {
	return t.addEvent(EvNodeUpdated, node)
}

func (t *TestHandler) NodeRemoved(node *v12.Node) error {
	return t.addEvent(EvNodeRemoved, node)
}
