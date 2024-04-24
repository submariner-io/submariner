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
	"fmt"
	"sync"

	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	v12 "k8s.io/api/core/v1"
)

type TestEvent struct {
	Name      string
	Handler   string
	Parameter interface{}
}

type TestHandlerState struct {
	event.DefaultHandlerState
	Gateway bool
}

func (c *TestHandlerState) IsOnGateway() bool {
	return c.Gateway
}

type TestHandlerBase struct {
	event.HandlerBase
	Name          string
	NetworkPlugin string
	Events        chan TestEvent
	Initialized   bool
	failOnEvent   sync.Map
}

type TestHandler struct {
	TestHandlerBase
}

type TestEndpointsOnlyHandler struct {
	TestHandlerBase
}

func NewTestHandler(name, networkPlugin string, events chan TestEvent) *TestHandler {
	return &TestHandler{
		TestHandlerBase{
			Name:          name,
			NetworkPlugin: networkPlugin,
			Events:        events,
		},
	}
}

func NewEndpointsOnlyHandler(name, networkPlugin string, events chan TestEvent) *TestEndpointsOnlyHandler {
	return &TestEndpointsOnlyHandler{
		TestHandlerBase{
			Name:          name,
			NetworkPlugin: networkPlugin,
			Events:        events,
		},
	}
}

func (t *TestHandlerBase) FailOnEvent(eventName ...string) {
	for _, e := range eventName {
		t.failOnEvent.Store(e, true)
	}
}

func (t *TestHandlerBase) checkFailOnEvent(eventName string) error {
	fail, ok := t.failOnEvent.LoadAndDelete(eventName)
	if ok && fail.(bool) {
		return fmt.Errorf("mock handler error for %q", eventName)
	}

	return nil
}

func (t *TestHandlerBase) addEvent(eventName string, param interface{}) error {
	if err := t.checkFailOnEvent(eventName); err != nil {
		return err
	}

	ev := TestEvent{
		Name:      eventName,
		Parameter: param,
		Handler:   t.Name,
	}

	t.Events <- ev

	return nil
}

func (t *TestHandlerBase) Init() error {
	t.Initialized = true
	return t.checkFailOnEvent(EvInit)
}

func (t *TestHandlerBase) GetName() string {
	return t.Name
}

func (t *TestHandlerBase) GetNetworkPlugins() []string {
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
	EvInit                   = "Init"
)

func (t *TestHandlerBase) Stop() error {
	return t.addEvent(EvStop, nil)
}

func (t *TestHandlerBase) Uninstall() error {
	return t.addEvent(EvUninstall, nil)
}

func (t *TestHandlerBase) TransitionToNonGateway() error {
	return t.addEvent(EvTransitionToNonGateway, nil)
}

func (t *TestHandlerBase) TransitionToGateway() error {
	return t.addEvent(EvTransitionToGateway, nil)
}

func (t *TestHandlerBase) LocalEndpointCreated(endpoint *v1.Endpoint) error {
	return t.addEvent(EvLocalEndpointCreated, endpoint)
}

func (t *TestHandlerBase) LocalEndpointUpdated(endpoint *v1.Endpoint) error {
	return t.addEvent(EvLocalEndpointUpdated, endpoint)
}

func (t *TestHandlerBase) LocalEndpointRemoved(endpoint *v1.Endpoint) error {
	return t.addEvent(EvLocalEndpointRemoved, endpoint)
}

func (t *TestHandlerBase) RemoteEndpointCreated(endpoint *v1.Endpoint) error {
	return t.addEvent(EvRemoteEndpointCreated, endpoint)
}

func (t *TestHandlerBase) RemoteEndpointUpdated(endpoint *v1.Endpoint) error {
	return t.addEvent(EvRemoteEndpointUpdated, endpoint)
}

func (t *TestHandlerBase) RemoteEndpointRemoved(endpoint *v1.Endpoint) error {
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
