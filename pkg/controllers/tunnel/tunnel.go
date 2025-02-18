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
	k8snet "k8s.io/utils/net"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type controller struct {
	engine          cableengine.Engine
	localIPFamilies [2]k8snet.IPFamily
}

var logger = log.Logger{Logger: logf.Log.WithName("Tunnel")}

func findCommonIPFamilies(local, remote [2]k8snet.IPFamily) []k8snet.IPFamily {
	common := []k8snet.IPFamily{}

	for _, lf := range local {
		for _, rf := range remote {
			if lf == rf {
				common = append(common, lf)
				break
			}
		}
	}

	return common
}

func StartController(engine cableengine.Engine, namespace string, config *watcher.Config, stopCh <-chan struct{}) error {
	logger.Info("Starting the tunnel controller")

	c := &controller{engine: engine}

	c.localIPFamilies = c.engine.GetLocalEndpoint().Spec.GetIPFamilies()

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

func (c *controller) handleCreatedOrUpdatedEndpoint(obj runtime.Object, _ int) bool {
	endpoint := obj.(*v1.Endpoint)

	logger.V(log.TRACE).Infof("Tunnel controller processing added or updated submariner Endpoint object: %#v", endpoint)

	commonIPFamilies := findCommonIPFamilies(c.localIPFamilies, endpoint.Spec.GetIPFamilies())

	var errs []error

	for _, family := range commonIPFamilies {
		err := c.engine.InstallCable(endpoint, family)
		if err != nil {
			logger.Errorf(err, "Error installing IPv%v cable for Endpoint %#v", family, endpoint)
			errs = append(errs, err)
		}
	}

	return len(errs) > 0
}

func (c *controller) handleRemovedEndpoint(obj runtime.Object, _ int) bool {
	endpoint := obj.(*v1.Endpoint)

	commonIPFamilies := findCommonIPFamilies(c.localIPFamilies, endpoint.Spec.GetIPFamilies())

	logger.V(log.DEBUG).Infof("Tunnel controller processing removed submariner Endpoint object: %#v", endpoint)

	var errs []error

	for _, family := range commonIPFamilies {
		if err := c.engine.RemoveCable(endpoint, family); err != nil {
			logger.Errorf(err, "Tunnel controller failed to remove Endpoint IPv%v cable %#v from the engine", family, endpoint)
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return true
	}

	logger.V(log.DEBUG).Infof("Tunnel controller processing removed submariner Endpoint object: %#v", endpoint)

	return false
}
