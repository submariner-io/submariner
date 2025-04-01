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
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/util"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	v1typed "github.com/submariner-io/submariner/pkg/client/clientset/versioned/typed/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/pinger"
	apiError "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	k8snet "k8s.io/utils/net"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type Config struct {
	pinger.ControllerConfig
	HealthCheckerEnabled     bool
	RouteAgentUpdateInterval time.Duration
}

type controller struct {
	event.HandlerBase
	pingerController pinger.Controller
	localNodeName    string
	version          string
	config           Config
	stopCh           chan struct{}
	stopOnce         sync.Once
	client           v1typed.RouteAgentInterface
}

var logger = log.Logger{Logger: logf.Log.WithName("HealthChecker")}

func New(config *Config, client v1typed.RouteAgentInterface, version, nodeName string) event.Handler {
	return &controller{
		pingerController: pinger.NewController(config.ControllerConfig),
		config:           *config,
		version:          version,
		client:           client,
		stopCh:           make(chan struct{}),
		localNodeName:    nodeName,
	}
}

func (h *controller) Stop() error {
	h.pingerController.Stop()

	h.stopOnce.Do(func() {
		if h.stopCh != nil {
			close(h.stopCh)
			h.stopCh = nil
		}
	})

	err := h.client.Delete(context.TODO(),
		h.localNodeName, metav1.DeleteOptions{})
	if err != nil && !apiError.IsNotFound(err) {
		return errors.Wrapf(err, "Error deleting RouteAgent: %s", h.localNodeName)
	}

	return nil
}

func (h *controller) RemoteEndpointCreated(endpoint *submarinerv1.Endpoint) error {
	return h.RemoteEndpointUpdated(endpoint)
}

func (h *controller) RemoteEndpointUpdated(endpoint *submarinerv1.Endpoint) error {
	if !h.config.HealthCheckerEnabled || h.State().IsOnGateway() {
		return nil
	}

	h.processEndpointCreatedOrUpdated(endpoint)

	return nil
}

func (h *controller) processEndpointCreatedOrUpdated(endpoint *submarinerv1.Endpoint) {
	logger.Infof("Processing Endpoint: %#v", endpoint)

	h.pingerController.EndpointCreatedOrUpdated(&endpoint.Spec)
}

func (h *controller) RemoteEndpointRemoved(endpoint *submarinerv1.Endpoint) error {
	logger.Infof("Processing removed Endpoint %q", endpoint.Spec.CableName)

	h.pingerController.EndpointRemoved(&endpoint.Spec)

	return nil
}

func (h *controller) StaleRemoteEndpointRemoved(endpoint *submarinerv1.Endpoint) error {
	return h.RemoteEndpointRemoved(endpoint)
}

func (h *controller) Init(_ context.Context) error {
	//nolint:contextcheck // Ignore "should pass the context parameter"
	go func() {
		wait.Until(func() {
			h.syncRouteAgentStatus()
		}, h.config.RouteAgentUpdateInterval, h.stopCh)
	}()

	return nil
}

// TransitionToNonGateway is called once for each transition of the local node from Gateway to a non-Gateway.
func (h *controller) TransitionToNonGateway() error {
	if h.config.HealthCheckerEnabled {
		remoteEndpoints := h.State().GetRemoteEndpoints()

		for i := range remoteEndpoints {
			h.processEndpointCreatedOrUpdated(&remoteEndpoints[i])
		}
	}

	return nil
}

// TransitionToGateway is called once for each transition of the local node from non-Gateway to a Gateway.
func (h *controller) TransitionToGateway() error {
	if h.config.HealthCheckerEnabled {
		h.pingerController.Stop()
	}

	h.syncRouteAgentStatus()

	return nil
}

func (h *controller) GetNetworkPlugins() []string {
	return []string{event.AnyNetworkPlugin}
}

func (h *controller) GetName() string {
	return "routeAgent-health-checker"
}

func (h *controller) syncRouteAgentStatus() {
	routeAgent := h.generateRouteAgentObject()
	remoteEndpoints := h.State().GetRemoteEndpoints()

	for i := range remoteEndpoints {
		var connectionStatus submarinerv1.ConnectionStatus
		var remoteEndpoint submarinerv1.RemoteEndpoint
		var statusMessage string
		var latencyRTT *submarinerv1.LatencyRTTSpec

		if !h.config.HealthCheckerEnabled {
			connectionStatus = submarinerv1.ConnectionNone
			statusMessage = "Health check is not enabled"
		} else if h.State().IsOnGateway() {
			connectionStatus = submarinerv1.ConnectionNone
			statusMessage = "Health check is not performed on gateway nodes"
		} else if pingerObject := h.pingerController.Get(&remoteEndpoints[i].Spec, k8snet.IPv4); pingerObject != nil {
			latencyInfo := pingerObject.GetLatencyInfo()
			if latencyInfo != nil {
				switch latencyInfo.ConnectionStatus {
				case pinger.Connected:
					connectionStatus = submarinerv1.Connected
					statusMessage = ""
					latencyRTT = &submarinerv1.LatencyRTTSpec{
						Last:    latencyInfo.Spec.Last,
						Min:     latencyInfo.Spec.Min,
						Average: latencyInfo.Spec.Average,
						Max:     latencyInfo.Spec.Max,
						StdDev:  latencyInfo.Spec.StdDev,
					}
				case pinger.ConnectionError, pinger.ConnectionUnknown:
					connectionStatus = submarinerv1.ConnectionError
					statusMessage = latencyInfo.ConnectionError
				}
			} else {
				connectionStatus = submarinerv1.Connecting
				statusMessage = ""
			}
		} else {
			connectionStatus = submarinerv1.ConnectionNone
			statusMessage = "Health checker IP is not configured"
		}

		remoteEndpoint = submarinerv1.RemoteEndpoint{
			Status:        connectionStatus,
			StatusMessage: statusMessage,
			Spec:          remoteEndpoints[i].Spec,
			LatencyRTT:    latencyRTT,
		}

		routeAgent.Status.RemoteEndpoints = append(routeAgent.Status.RemoteEndpoints, remoteEndpoint)
	}
	// Use CreateOrUpdate to handle the RouteAgent resource
	_, err := util.CreateOrUpdate(context.TODO(), h.routeAgentResourceInterface(), routeAgent,
		func(existing *submarinerv1.RouteAgent) (*submarinerv1.RouteAgent, error) {
			existing.Status = routeAgent.Status

			return existing, nil
		})
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("error creating/updating RouteAgent: %w", err))
		return
	}
}

func (h *controller) generateRouteAgentObject() *submarinerv1.RouteAgent {
	return &submarinerv1.RouteAgent{
		ObjectMeta: metav1.ObjectMeta{
			Name: h.localNodeName,
		},
		Status: submarinerv1.RouteAgentStatus{
			Version:         h.version,
			StatusFailure:   "",
			RemoteEndpoints: []submarinerv1.RemoteEndpoint{},
		},
	}
}

func (h *controller) routeAgentResourceInterface() resource.Interface[*submarinerv1.RouteAgent] {
	return &resource.InterfaceFuncs[*submarinerv1.RouteAgent]{
		GetFunc:    h.client.Get,
		CreateFunc: h.client.Create,
		UpdateFunc: h.client.Update,
		DeleteFunc: h.client.Delete,
	}
}
