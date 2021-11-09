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

package tunnel

import (
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/watcher"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cableengine"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
)

type controller struct {
	engine cableengine.Engine
}

func StartController(engine cableengine.Engine, namespace string, config *watcher.Config, stopCh <-chan struct{}) error {
	klog.Info("Starting the tunnel controller")

	c := &controller{engine: engine}

	config.ResourceConfigs = []watcher.ResourceConfig{
		{
			Name:         "Tunnel Controller",
			ResourceType: &v1.Endpoint{},
			Handler: watcher.EventHandlerFuncs{
				OnCreateFunc: c.handleCreatedOrUpdatedEndpoint,
				OnUpdateFunc: c.handleCreatedOrUpdatedEndpoint,
				OnDeleteFunc: c.handleRemovedEndpoint,
			},
			SourceNamespace: namespace,
		},
	}

	if config.ResyncPeriod == 0 {
		config.ResyncPeriod = time.Second * 30
	}

	endpointWatcher, err := watcher.New(config)
	if err != nil {
		return errors.Wrap(err, "error creating the Endpoint watcher")
	}

	err = endpointWatcher.Start(stopCh)
	if err != nil {
		return errors.Wrap(err, "error starting the Endpoint watcher")
	}

	return nil
}

func (c *controller) handleCreatedOrUpdatedEndpoint(obj runtime.Object, numRequeues int) bool {
	endpoint := obj.(*v1.Endpoint)

	klog.V(log.DEBUG).Infof("Tunnel controller processing added or updated submariner Endpoint object: %#v", endpoint)

	err := c.engine.InstallCable(endpoint)
	if err != nil {
		klog.Errorf("error installing cable for Endpoint %#v, %v", endpoint, err)
		return true
	}

	return false
}

func (c *controller) handleRemovedEndpoint(obj runtime.Object, numRequeues int) bool {
	endpoint := obj.(*v1.Endpoint)

	klog.V(log.DEBUG).Infof("Tunnel controller processing removed submariner Endpoint object: %#v", endpoint)

	if err := c.engine.RemoveCable(endpoint); err != nil {
		klog.Errorf("Tunnel controller failed to remove Endpoint cable %#v from the engine: %v", endpoint, err)
		return true
	}

	klog.V(log.DEBUG).Infof("Tunnel controller successfully removed Endpoint cable %s from the engine", endpoint.Spec.CableName)

	return false
}
