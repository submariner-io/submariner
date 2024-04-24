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
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	k8serrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/set"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type Registry struct {
	name          string
	networkPlugin string
	eventHandlers []Handler
}

var logger = log.Logger{Logger: logf.Log.WithName("EventRegistry")}

// NewRegistry creates a new registry with the given name, typically referencing the owner, to manage event
// Handlers that match the given networkPlugin name. The given event Handlers whose associated network plugin matches the given
// networkPlugin name are added. Non-matching Handlers are ignored. Handlers will be called in registration order.
func NewRegistry(name, networkPlugin string, eventHandlers ...Handler) (*Registry, error) {
	r := &Registry{
		name:          name,
		networkPlugin: strings.ToLower(networkPlugin),
		eventHandlers: []Handler{},
	}

	for _, eventHandler := range eventHandlers {
		err := r.addHandler(eventHandler)
		if err != nil {
			return nil, err
		}
	}

	return r, nil
}

// GetName returns the name of the registry.
func (er *Registry) GetName() string {
	return er.name
}

func (er *Registry) addHandler(eventHandler Handler) error {
	evNetworkPlugins := set.New[string]()

	for _, np := range eventHandler.GetNetworkPlugins() {
		evNetworkPlugins.Insert(strings.ToLower(np))
	}

	if evNetworkPlugins.Has(AnyNetworkPlugin) || evNetworkPlugins.Has(er.networkPlugin) {
		if err := eventHandler.Init(); err != nil {
			return errors.Wrapf(err, "Event handler %q failed to initialize", eventHandler.GetName())
		}

		er.eventHandlers = append(er.eventHandlers, eventHandler)
		logger.Infof("Event handler %q added to registry %q.", eventHandler.GetName(), er.name)
	} else {
		logger.V(log.DEBUG).Infof("Event handler %q ignored for registry %q as networkPlugin is %q.",
			eventHandler.GetName(), er.name, er.networkPlugin)
	}

	return nil
}

func (er *Registry) GetHandlers() []Handler {
	return er.eventHandlers
}

func (er *Registry) StopHandlers() error {
	return er.invokeHandlers("Stop", func(h Handler) error {
		return h.Stop()
	})
}

func (er *Registry) Uninstall() error {
	return er.invokeHandlers("Uninstall", func(h Handler) error {
		return h.Uninstall()
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
