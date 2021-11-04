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
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/stringset"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	k8sV1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog"
)

type Registry struct {
	name          string
	networkPlugin string
	eventHandlers []Handler
}

// NewRegistry creates a new registry with the given name,  typically referencing the owner, to manage event
// Handlers that match the given networkPlugin name.
func NewRegistry(name, networkPlugin string) *Registry {
	return &Registry{
		name:          name,
		networkPlugin: networkPlugin,
		eventHandlers: []Handler{},
	}
}

// GetName returns the name of the registry.
func (er *Registry) GetName() string {
	return er.name
}

func (er *Registry) addHandler(eventHandler Handler) error {
	evNetworkPlugins := stringset.New()

	for _, np := range eventHandler.GetNetworkPlugins() {
		evNetworkPlugins.Add(np)
	}

	if evNetworkPlugins.Contains(AnyNetworkPlugin) || evNetworkPlugins.Contains(er.networkPlugin) {
		if err := eventHandler.Init(); err != nil {
			return errors.Wrapf(err, "Event handler %q failed to initialize", eventHandler.GetName())
		}

		er.eventHandlers = append(er.eventHandlers, eventHandler)
		klog.Infof("Event handler %q added to registry %q.", eventHandler.GetName(), er.name)
	} else {
		klog.V(log.DEBUG).Infof("Event handler %q ignored for registry %q.", eventHandler.GetName(), er.name)
	}

	return nil
}

// AddHandlers adds the given event Handlers whose associated network plugin matches the network plugin
// associated with this registry. Non-matching Handlers are ignored. Handlers will be called in registration order.
func (er *Registry) AddHandlers(eventHandlers ...Handler) error {
	for _, eventHandler := range eventHandlers {
		err := er.addHandler(eventHandler)
		if err != nil {
			return err
		}
	}

	return nil
}

func (er *Registry) StopHandlers(uninstall bool) error {
	return er.invokeHandlers("Stop", func(h Handler) error {
		return h.Stop(uninstall)
	})
}

func (er *Registry) TransitionToNonGateway() error {
	return er.invokeHandlers("TransitionToNonGateway", func(h Handler) error {
		return h.TransitionToNonGateway()
	})
}

func (er *Registry) TransitionToGateway() error {
	return er.invokeHandlers("TransitionToGateway", func(h Handler) error {
		return h.TransitionToGateway()
	})
}

func (er *Registry) LocalEndpointCreated(endpoint *submV1.Endpoint) error {
	return er.invokeHandlers("LocalEndpointCreated", func(h Handler) error {
		return h.LocalEndpointCreated(endpoint)
	})
}

func (er *Registry) LocalEndpointUpdated(endpoint *submV1.Endpoint) error {
	return er.invokeHandlers("LocalEndpointUpdated", func(h Handler) error {
		return h.LocalEndpointUpdated(endpoint)
	})
}

func (er *Registry) LocalEndpointRemoved(endpoint *submV1.Endpoint) error {
	return er.invokeHandlers("LocalEndpointRemoved", func(h Handler) error {
		return h.LocalEndpointRemoved(endpoint)
	})
}

func (er *Registry) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	return er.invokeHandlers("RemoteEndpointCreated", func(h Handler) error {
		return h.RemoteEndpointCreated(endpoint)
	})
}

func (er *Registry) RemoteEndpointUpdated(endpoint *submV1.Endpoint) error {
	return er.invokeHandlers("RemoteEndpointUpdated", func(h Handler) error {
		return h.RemoteEndpointUpdated(endpoint)
	})
}

func (er *Registry) RemoteEndpointRemoved(endpoint *submV1.Endpoint) error {
	return er.invokeHandlers("RemoteEndpointRemoved", func(h Handler) error {
		return h.RemoteEndpointRemoved(endpoint)
	})
}

func (er *Registry) NodeCreated(node *k8sV1.Node) error {
	return er.invokeHandlers("NodeCreated", func(h Handler) error {
		return h.NodeCreated(node)
	})
}

func (er *Registry) NodeUpdated(node *k8sV1.Node) error {
	return er.invokeHandlers("NodeUpdated", func(h Handler) error {
		return h.NodeUpdated(node)
	})
}

func (er *Registry) NodeRemoved(node *k8sV1.Node) error {
	return er.invokeHandlers("NodeRemoved", func(h Handler) error {
		return h.NodeRemoved(node)
	})
}

func (er *Registry) invokeHandlers(eventName string, invoke func(h Handler) error) error {
	var errs []error

	for _, h := range er.eventHandlers {
		err := invoke(h)
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "%q returned error", h.GetName()))
		}
	}

	return errors.Wrapf(k8serrors.NewAggregate(errs), "%s failed", eventName)
}
