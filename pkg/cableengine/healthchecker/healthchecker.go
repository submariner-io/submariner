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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
)

type LatencyInfo struct {
	ConnectionError  string
	ConnectionStatus ConnectionStatus
	Spec             *submarinerv1.LatencyRTTSpec
}

type ConnectionStatus string

const (
	Connected         ConnectionStatus = "connected"
	ConnectionUnknown ConnectionStatus = "unknown"
	ConnectionError   ConnectionStatus = "error"
)

type Interface interface {
	Start(stopCh <-chan struct{}) error
	GetLatencyInfo(endpoint *submarinerv1.EndpointSpec) *LatencyInfo
}

type Config struct {
	WatcherConfig      *watcher.Config
	EndpointNamespace  string
	ClusterID          string
	PingInterval       uint
	MaxPacketLossCount uint
	NewPinger          func(PingerConfig) PingerInterface
}

type controller struct {
	endpointWatcher watcher.Interface
	pingers         sync.Map
	config          *Config
}

func New(config *Config) (Interface, error) {
	controller := &controller{
		config: config,
	}

	config.WatcherConfig.ResourceConfigs = []watcher.ResourceConfig{
		{
			Name:         "HealthChecker Endpoint Controller",
			ResourceType: &submarinerv1.Endpoint{},
			Handler: watcher.EventHandlerFuncs{
				OnCreateFunc: controller.endpointCreatedorUpdated,
				OnUpdateFunc: controller.endpointCreatedorUpdated,
				OnDeleteFunc: controller.endpointDeleted,
			},
			SourceNamespace: config.EndpointNamespace,
		},
	}

	var err error

	controller.endpointWatcher, err = watcher.New(config.WatcherConfig)
	if err != nil {
		return nil, errors.Wrapf(err, "error creating watcher")
	}

	return controller, nil
}

func (h *controller) GetLatencyInfo(endpoint *submarinerv1.EndpointSpec) *LatencyInfo {
	if obj, found := h.pingers.Load(endpoint.CableName); found {
		return obj.(PingerInterface).GetLatencyInfo()
	}

	return nil
}

func (h *controller) Start(stopCh <-chan struct{}) error {
	if err := h.endpointWatcher.Start(stopCh); err != nil {
		return errors.Wrapf(err, "error starting watcher")
	}

	klog.Infof("CableEngine HealthChecker started with PingInterval: %v, MaxPacketLossCount: %v", h.config.PingInterval,
		h.config.MaxPacketLossCount)

	return nil
}

func (h *controller) endpointCreatedorUpdated(obj runtime.Object, numRequeues int) bool {
	klog.V(log.TRACE).Infof("Endpoint created: %#v", obj)

	endpointCreated := obj.(*submarinerv1.Endpoint)
	if endpointCreated.Spec.ClusterID == h.config.ClusterID {
		return false
	}

	if endpointCreated.Spec.HealthCheckIP == "" || endpointCreated.Spec.CableName == "" {
		klog.Infof("HealthCheckIP (%q) and/or CableName (%q) for Endpoint %q empty - will not monitor endpoint health",
			endpointCreated.Spec.HealthCheckIP, endpointCreated.Spec.CableName, endpointCreated.Name)
		return false
	}

	if obj, found := h.pingers.Load(endpointCreated.Spec.CableName); found {
		pinger := obj.(PingerInterface)
		if pinger.GetIP() == endpointCreated.Spec.HealthCheckIP {
			return false
		}

		klog.V(log.DEBUG).Infof("HealthChecker is already running for %q - stopping", endpointCreated.Name)
		pinger.Stop()
		h.pingers.Delete(endpointCreated.Spec.CableName)
	}

	pingerConfig := PingerConfig{
		IP:                 endpointCreated.Spec.HealthCheckIP,
		MaxPacketLossCount: h.config.MaxPacketLossCount,
	}

	if h.config.PingInterval != 0 {
		pingerConfig.Interval = time.Second * time.Duration(h.config.PingInterval)
	}

	newPingerFunc := h.config.NewPinger
	if newPingerFunc == nil {
		newPingerFunc = NewPinger
	}

	pinger := newPingerFunc(pingerConfig)
	h.pingers.Store(endpointCreated.Spec.CableName, pinger)
	pinger.Start()

	klog.Infof("CableEngine HealthChecker started pinger for CableName: %q with HealthCheckIP %q",
		endpointCreated.Spec.CableName, endpointCreated.Spec.HealthCheckIP)

	return false
}

func (h *controller) endpointDeleted(obj runtime.Object, numRequeues int) bool {
	endpointDeleted := obj.(*submarinerv1.Endpoint)
	if obj, found := h.pingers.Load(endpointDeleted.Spec.CableName); found {
		pinger := obj.(PingerInterface)
		pinger.Stop()
		h.pingers.Delete(endpointDeleted.Spec.CableName)
	}

	return false
}
