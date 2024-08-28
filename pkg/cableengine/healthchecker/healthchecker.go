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
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/pinger"
	"k8s.io/apimachinery/pkg/runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type Interface interface {
	Start(stopCh <-chan struct{}) error
	GetLatencyInfo(endpoint *submarinerv1.EndpointSpec) *pinger.LatencyInfo
	Stop()
}

type Config struct {
	WatcherConfig      *watcher.Config
	EndpointNamespace  string
	ClusterID          string
	PingInterval       uint
	MaxPacketLossCount uint
	NewPinger          func(pinger.Config) pinger.Interface
}

type controller struct {
	sync.RWMutex
	pingers map[string]pinger.Interface
	config  *Config
}

var logger = log.Logger{Logger: logf.Log.WithName("HealthChecker")}

func New(config *Config) (Interface, error) {
	controller := &controller{
		config:  config,
		pingers: map[string]pinger.Interface{},
	}

	config.WatcherConfig.ResourceConfigs = []watcher.ResourceConfig{
		{
			Name:         "HealthChecker Endpoint Controller",
			ResourceType: &submarinerv1.Endpoint{},
			Handler: watcher.EventHandlerFuncs{
				OnCreateFunc: controller.endpointCreatedOrUpdated,
				OnUpdateFunc: controller.endpointCreatedOrUpdated,
				OnDeleteFunc: controller.endpointDeleted,
			},
			SourceNamespace: config.EndpointNamespace,
		},
	}

	return controller, nil
}

func (h *controller) GetLatencyInfo(endpoint *submarinerv1.EndpointSpec) *pinger.LatencyInfo {
	h.RLock()
	defer h.RUnlock()

	if pingerObject, found := h.pingers[endpoint.CableName]; found {
		return pingerObject.GetLatencyInfo()
	}

	return nil
}

func (h *controller) Start(stopCh <-chan struct{}) error {
	endpointWatcher, err := watcher.New(h.config.WatcherConfig)
	if err != nil {
		return errors.Wrapf(err, "error creating watcher")
	}

	if err := endpointWatcher.Start(stopCh); err != nil {
		return errors.Wrapf(err, "error starting watcher")
	}

	logger.Infof("CableEngine HealthChecker started with PingInterval: %v, MaxPacketLossCount: %v", h.config.PingInterval,
		h.config.MaxPacketLossCount)

	return nil
}

func (h *controller) Stop() {
	h.Lock()
	defer h.Unlock()

	for _, p := range h.pingers {
		p.Stop()
	}

	h.pingers = map[string]pinger.Interface{}
}

func (h *controller) endpointCreatedOrUpdated(obj runtime.Object, _ int) bool {
	logger.V(log.TRACE).Infof("Endpoint created: %#v", obj)

	endpointCreated := obj.(*submarinerv1.Endpoint)
	if endpointCreated.Spec.ClusterID == h.config.ClusterID {
		return false
	}

	if endpointCreated.Spec.HealthCheckIP == "" || endpointCreated.Spec.CableName == "" {
		logger.Infof("HealthCheckIP (%q) and/or CableName (%q) for Endpoint %q empty - will not monitor endpoint health",
			endpointCreated.Spec.HealthCheckIP, endpointCreated.Spec.CableName, endpointCreated.Name)
		return false
	}

	h.Lock()
	defer h.Unlock()

	if pingerObject, found := h.pingers[endpointCreated.Spec.CableName]; found {
		if pingerObject.GetIP() == endpointCreated.Spec.HealthCheckIP {
			return false
		}

		logger.V(log.DEBUG).Infof("HealthChecker is already running for %q - stopping", endpointCreated.Name)
		pingerObject.Stop()
		delete(h.pingers, endpointCreated.Spec.CableName)
	}

	pingerConfig := pinger.Config{
		IP:                 endpointCreated.Spec.HealthCheckIP,
		MaxPacketLossCount: h.config.MaxPacketLossCount,
	}

	if h.config.PingInterval != 0 {
		pingerConfig.Interval = time.Second * time.Duration(h.config.PingInterval) //nolint:gosec // We can safely ignore integer conversion error
	}

	newPingerFunc := h.config.NewPinger
	if newPingerFunc == nil {
		newPingerFunc = pinger.NewPinger
	}

	pingerObject := newPingerFunc(pingerConfig)
	h.pingers[endpointCreated.Spec.CableName] = pingerObject
	pingerObject.Start()

	logger.Infof("CableEngine HealthChecker started pinger for CableName: %q with HealthCheckIP %q",
		endpointCreated.Spec.CableName, endpointCreated.Spec.HealthCheckIP)

	return false
}

func (h *controller) endpointDeleted(obj runtime.Object, _ int) bool {
	endpointDeleted := obj.(*submarinerv1.Endpoint)

	h.Lock()
	defer h.Unlock()

	if pingerObject, found := h.pingers[endpointDeleted.Spec.CableName]; found {
		pingerObject.Stop()
		delete(h.pingers, endpointDeleted.Spec.CableName)
	}

	return false
}
