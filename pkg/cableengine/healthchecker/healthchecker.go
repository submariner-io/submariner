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

package healthchecker

import (
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/pinger"
	"k8s.io/apimachinery/pkg/runtime"
	k8snet "k8s.io/utils/net"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type Interface interface {
	Start(stopCh <-chan struct{}) error
	GetLatencyInfo(endpoint *submarinerv1.EndpointSpec, ipFamily k8snet.IPFamily) *pinger.LatencyInfo
	Stop()
}

type Config struct {
	pinger.ControllerConfig
	WatcherConfig     watcher.Config
	EndpointNamespace string
	ClusterID         string
}

type controller struct {
	config           Config
	pingerController pinger.Controller
}

var logger = log.Logger{Logger: logf.Log.WithName("HealthChecker")}

func New(config *Config) (Interface, error) {
	c := &controller{
		config:           *config,
		pingerController: pinger.NewController(config.ControllerConfig),
	}

	c.config.WatcherConfig.ResourceConfigs = []watcher.ResourceConfig{
		{
			Name:         "HealthChecker Endpoint Controller",
			ResourceType: &submarinerv1.Endpoint{},
			Handler: watcher.EventHandlerFuncs{
				OnCreateFunc: c.endpointCreatedOrUpdated,
				OnUpdateFunc: c.endpointCreatedOrUpdated,
				OnDeleteFunc: c.endpointDeleted,
			},
			SourceNamespace: config.EndpointNamespace,
		},
	}

	return c, nil
}

func (h *controller) GetLatencyInfo(endpoint *submarinerv1.EndpointSpec, ipFamily k8snet.IPFamily) *pinger.LatencyInfo {
	pinger := h.pingerController.Get(endpoint, ipFamily)
	if pinger != nil {
		return pinger.GetLatencyInfo()
	}

	return nil
}

func (h *controller) Start(stopCh <-chan struct{}) error {
	endpointWatcher, err := watcher.New(&h.config.WatcherConfig)
	if err != nil {
		return errors.Wrapf(err, "error creating watcher")
	}

	if err := endpointWatcher.Start(stopCh); err != nil {
		return errors.Wrapf(err, "error starting watcher")
	}

	logger.Infof("CableEngine HealthChecker started with SupportedIPFamilies: %q, PingInterval: %v, MaxPacketLossCount: %v",
		h.config.SupportedIPFamilies, h.config.PingInterval, h.config.MaxPacketLossCount)

	return nil
}

func (h *controller) Stop() {
	h.pingerController.Stop()
}

func (h *controller) endpointCreatedOrUpdated(obj runtime.Object, _ int) bool {
	logger.V(log.TRACE).Infof("Endpoint created: %#v", obj)

	endpointCreated := obj.(*submarinerv1.Endpoint)
	if endpointCreated.Spec.ClusterID == h.config.ClusterID {
		return false
	}

	h.pingerController.EndpointCreatedOrUpdated(&endpointCreated.Spec)

	return false
}

func (h *controller) endpointDeleted(obj runtime.Object, _ int) bool {
	h.pingerController.EndpointRemoved(&(obj.(*submarinerv1.Endpoint)).Spec)
	return false
}
