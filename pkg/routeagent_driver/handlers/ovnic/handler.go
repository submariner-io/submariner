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

package ovnic

import (
	"sync"

	"github.com/submariner-io/admiral/pkg/log"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	clientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/environment"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type Handler struct {
	event.HandlerBase
	mutex           sync.Mutex
	config          *environment.Specification
	smClient        clientset.Interface
	localEndpoint   *submV1.Endpoint
	remoteEndpoints map[string]*submV1.Endpoint
}

var logger = log.Logger{Logger: logf.Log.WithName("OVN-IC")}

func NewHandler(env *environment.Specification, smClientSet clientset.Interface) *Handler {
	return &Handler{
		config:          env,
		smClient:        smClientSet,
		remoteEndpoints: map[string]*submV1.Endpoint{},
	}
}

func (ovn *Handler) GetName() string {
	return "ovn-ic-handler"
}

func (ovn *Handler) GetNetworkPlugins() []string {
	// TODO: Once we have the necessary controllers implemented for OVN-IC support, the network plugin name
	// should be updated, until then, we do not want this handler to be registered even for OVN deployments.
	return []string{"OVN-IC"}
}

func (ovn *Handler) Init() error {
	return nil
}

func (ovn *Handler) LocalEndpointCreated(endpoint *submV1.Endpoint) error {
	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	logger.Infof("A new Endpoint for the local cluster has been created: %#v", endpoint.Spec)
	ovn.localEndpoint = endpoint

	return nil
}

func (ovn *Handler) LocalEndpointUpdated(endpoint *submV1.Endpoint) error {
	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	logger.Infof("A new Endpoint for the local cluster has been updated: %#v", endpoint.Spec)
	ovn.localEndpoint = endpoint

	return nil
}

func (ovn *Handler) LocalEndpointRemoved(endpoint *submV1.Endpoint) error {
	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	logger.Infof("A new Endpoint for the local cluster has been deleted: %#v", endpoint.Spec)

	if ovn.localEndpoint.Name == endpoint.Name {
		ovn.localEndpoint = nil
	}

	return nil
}

func (ovn *Handler) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	logger.Infof("A new Endpoint for the remote cluster has been created: %#v", endpoint.Spec)
	return nil
}
