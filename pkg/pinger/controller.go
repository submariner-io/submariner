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

package pinger

import (
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	k8snet "k8s.io/utils/net"
)

type Controller interface {
	EndpointCreatedOrUpdated(spec *submarinerv1.EndpointSpec)
	EndpointRemoved(spec *submarinerv1.EndpointSpec)
	Get(spec *submarinerv1.EndpointSpec, family k8snet.IPFamily) Interface
	Stop()
}

type ControllerConfig struct {
	SupportedIPFamilies []k8snet.IPFamily
	PingInterval        int
	MaxPacketLossCount  int
	NewPinger           func(Config) Interface
}

type controller struct {
	ControllerConfig
	sync.RWMutex
	pingers map[string]Interface
}

func NewController(config ControllerConfig) Controller {
	if len(config.SupportedIPFamilies) == 0 {
		panic(errors.New("SupportedIPFamilies must not be empty"))
	}

	return &controller{
		ControllerConfig: config,
		pingers:          map[string]Interface{},
	}
}

func (c *controller) EndpointCreatedOrUpdated(spec *submarinerv1.EndpointSpec) {
	c.Lock()
	defer c.Unlock()

	for _, family := range c.SupportedIPFamilies {
		healthCheckIP := spec.GetHealthCheckIP(family)
		if healthCheckIP == "" {
			logger.Infof("IPv%v HealthCheckIP for Endpoint %q is empty - will not monitor endpoint health",
				family, spec.CableName)
			continue
		}

		c.startPinger(spec.GetFamilyCableName(family), healthCheckIP)
	}
}

func (c *controller) startPinger(familyCableName, healthCheckIP string) {
	if pingerObject, found := c.pingers[familyCableName]; found {
		if pingerObject.GetIP() == healthCheckIP {
			return
		}

		logger.V(log.DEBUG).Infof("HealthChecker is already running for %q - stopping", familyCableName)
		pingerObject.Stop()
		delete(c.pingers, familyCableName)
	}

	pingerConfig := Config{
		IP:                 healthCheckIP,
		MaxPacketLossCount: c.MaxPacketLossCount,
	}

	if c.PingInterval != 0 {
		pingerConfig.Interval = time.Second * time.Duration(c.PingInterval)
	}

	newPingerFunc := c.NewPinger
	if newPingerFunc == nil {
		newPingerFunc = NewPinger
	}

	pingerObject := newPingerFunc(pingerConfig)
	c.pingers[familyCableName] = pingerObject
	pingerObject.Start()

	logger.Infof("HealthChecker started pinger for CableName: %q with HealthCheckIP %q", familyCableName, healthCheckIP)
}

func (c *controller) EndpointRemoved(spec *submarinerv1.EndpointSpec) {
	c.Lock()
	defer c.Unlock()

	for _, family := range c.SupportedIPFamilies {
		familyCableName := spec.GetFamilyCableName(family)
		if pingerObject, found := c.pingers[familyCableName]; found {
			pingerObject.Stop()
			delete(c.pingers, familyCableName)
		}
	}
}

func (c *controller) Get(spec *submarinerv1.EndpointSpec, family k8snet.IPFamily) Interface {
	c.Lock()
	defer c.Unlock()

	return c.pingers[spec.GetFamilyCableName(family)]
}

func (c *controller) Stop() {
	c.Lock()
	defer c.Unlock()

	for _, p := range c.pingers {
		p.Stop()
	}

	c.pingers = map[string]Interface{}
}
