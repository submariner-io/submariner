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
	k8snet "k8s.io/utils/net"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type Interface interface {
	Start(stopCh <-chan struct{}) error
	GetLatencyInfo(endpoint *submarinerv1.EndpointSpec, ipFamily k8snet.IPFamily) *pinger.LatencyInfo
	Stop()
}

type Config struct {
	WatcherConfig       *watcher.Config
	SupportedIPFamilies []k8snet.IPFamily
	EndpointNamespace   string
	ClusterID           string
	PingInterval        int
	MaxPacketLossCount  int
	NewPinger           func(pinger.Config) pinger.Interface
}

type controller struct {
	sync.RWMutex
	pingers map[string]pinger.Interface
	config  *Config
}

var logger = log.Logger{Logger: logf.Log.WithName("HealthChecker")}

func New(config *Config) (Interface, error) {
	if len(config.SupportedIPFamilies) == 0 {
		return nil, errors.New("SupportedIPFamilies must not be empty")
	}

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

func (h *controller) GetLatencyInfo(endpoint *submarinerv1.EndpointSpec, ipFamily k8snet.IPFamily) *pinger.LatencyInfo {
	h.RLock()
	defer h.RUnlock()

	if pingerObject, found := h.pingers[endpoint.GetFamilyCableName(ipFamily)]; found {
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

	logger.Infof("CableEngine HealthChecker started with SupportedIPFamilies: %q, PingInterval: %v, MaxPacketLossCount: %v",
		h.config.SupportedIPFamilies, h.config.PingInterval, h.config.MaxPacketLossCount)

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

	h.Lock()
	defer h.Unlock()

	for _, family := range h.config.SupportedIPFamilies {
		healthCheckIP := endpointCreated.Spec.GetHealthCheckIP(family)
		if healthCheckIP == "" {
			logger.Infof("IPv%v HealthCheckIP for Endpoint %q is empty - will not monitor endpoint health",
				family, endpointCreated.Name)
			continue
		}

		h.startPinger(endpointCreated.Spec.GetFamilyCableName(family), healthCheckIP)
	}

	return false
}

func (h *controller) startPinger(familyCableName, healthCheckIP string) {
	if pingerObject, found := h.pingers[familyCableName]; found {
		if pingerObject.GetIP() == healthCheckIP {
			return
		}

		logger.V(log.DEBUG).Infof("HealthChecker is already running for %q - stopping", familyCableName)
		pingerObject.Stop()
		delete(h.pingers, familyCableName)
	}

	pingerConfig := pinger.Config{
		IP:                 healthCheckIP,
		MaxPacketLossCount: h.config.MaxPacketLossCount,
	}

	if h.config.PingInterval != 0 {
		pingerConfig.Interval = time.Second * time.Duration(h.config.PingInterval)
	}

	newPingerFunc := h.config.NewPinger
	if newPingerFunc == nil {
		newPingerFunc = pinger.NewPinger
	}

	pingerObject := newPingerFunc(pingerConfig)
	h.pingers[familyCableName] = pingerObject
	pingerObject.Start()

	logger.Infof("CableEngine HealthChecker started pinger for CableName: %q with HealthCheckIP %q", familyCableName, healthCheckIP)
}

func (h *controller) endpointDeleted(obj runtime.Object, _ int) bool {
	endpointDeleted := obj.(*submarinerv1.Endpoint)

	h.Lock()
	defer h.Unlock()

	for _, family := range h.config.SupportedIPFamilies {
		familyCableName := endpointDeleted.Spec.GetFamilyCableName(family)
		if pingerObject, found := h.pingers[familyCableName]; found {
			pingerObject.Stop()
			delete(h.pingers, familyCableName)
		}
	}

	return false
}
