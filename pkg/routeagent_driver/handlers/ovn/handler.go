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
package ovn

import (
	"net"
	"sync"

	"github.com/pkg/errors"
	"github.com/submariner-io/submariner/pkg/cidr"
	"k8s.io/klog"

	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable/wireguard"
	clientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/netlink"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/environment"
	"github.com/submariner-io/submariner/pkg/util"
)

type Handler struct {
	event.HandlerBase
	mutex                 sync.Mutex
	config                *environment.Specification
	smClient              clientset.Interface
	cableRoutingInterface *net.Interface
	localEndpoint         *submV1.Endpoint
	remoteEndpoints       map[string]*submV1.Endpoint
	isGateway             bool
	netlink               netlink.Interface
}

func NewHandler(env environment.Specification, smClientSet clientset.Interface) *Handler {
	return &Handler{
		config:          &env,
		smClient:        smClientSet,
		remoteEndpoints: map[string]*submV1.Endpoint{},
		netlink:         netlink.New(),
	}
}

func (ovn *Handler) GetName() string {
	return "ovn-hostroutes-handler"
}

func (ovn *Handler) GetNetworkPlugins() []string {
	return []string{constants.NetworkPluginOVNKubernetes}
}

func (ovn *Handler) Init() error {
	err := ovn.initIPtablesChains()
	if err != nil {
		return err
	}

	return ovn.ensureSubmarinerNodeBridge()
}

func (ovn *Handler) LocalEndpointCreated(endpoint *submV1.Endpoint) error {
	var routingInterface *net.Interface
	var err error

	// TODO: this logic belongs to the cabledrivers instead
	if endpoint.Spec.Backend == "wireguard" {
		//NOTE: This assumes that LocalEndpointCreated happens before than TransitionToGatewayNode
		if routingInterface, err = net.InterfaceByName(wireguard.DefaultDeviceName); err != nil {
			return errors.Wrapf(err, "Wireguard interface %s not found on the node.", wireguard.DefaultDeviceName)
		}
	} else {
		if routingInterface, err = util.GetDefaultGatewayInterface(); err != nil {
			klog.Fatalf("Unable to find the default interface on host: %s", err.Error())
		}
	}

	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	ovn.localEndpoint = endpoint
	ovn.cableRoutingInterface = routingInterface

	return nil
}

func (ovn *Handler) LocalEndpointUpdated(endpoint *submV1.Endpoint) error {
	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	ovn.localEndpoint = endpoint

	return nil
}

func (ovn *Handler) LocalEndpointRemoved(endpoint *submV1.Endpoint) error {
	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	if ovn.localEndpoint.Name == endpoint.Name {
		ovn.localEndpoint = nil
	}

	return nil
}

func (ovn *Handler) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	if err := cidr.OverlappingSubnets(ovn.config.ServiceCidr, ovn.config.ClusterCidr, endpoint.Spec.Subnets); err != nil {
		// Skip processing the endpoint when CIDRs overlap and return nil to avoid re-queuing.
		klog.Errorf("overlappingSubnets for new remote %#v returned error: %v", endpoint, err)
		return nil
	}

	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	ovn.remoteEndpoints[endpoint.Name] = endpoint

	err := ovn.updateHostNetworkDataplane()
	if err != nil {
		return errors.Wrapf(err, "updateHostNetworkDataplane returned error")
	}

	if ovn.isGateway {
		return ovn.updateGatewayDataplane()
	}

	return nil
}

func (ovn *Handler) RemoteEndpointUpdated(endpoint *submV1.Endpoint) error {
	if err := cidr.OverlappingSubnets(ovn.config.ServiceCidr, ovn.config.ClusterCidr, endpoint.Spec.Subnets); err != nil {
		// Skip processing the endpoint when CIDRs overlap and return nil to avoid re-queuing.
		klog.Errorf("overlappingSubnets for new remote %#v returned error: %v", endpoint, err)
		return nil
	}

	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	ovn.remoteEndpoints[endpoint.Name] = endpoint

	err := ovn.updateHostNetworkDataplane()
	if err != nil {
		return errors.Wrapf(err, "updateHostNetworkDataplane returned error")
	}

	if ovn.isGateway {
		return ovn.updateGatewayDataplane()
	}

	return nil
}

func (ovn *Handler) RemoteEndpointRemoved(endpoint *submV1.Endpoint) error {
	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	delete(ovn.remoteEndpoints, endpoint.Name)

	err := ovn.updateHostNetworkDataplane()
	if err != nil {
		return errors.Wrapf(err, "updateHostNetworkDataplane returned error")
	}

	if ovn.isGateway {
		return ovn.updateGatewayDataplane()
	}

	return nil
}

func (ovn *Handler) TransitionToNonGateway() error {
	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	ovn.isGateway = false

	return ovn.cleanupGatewayDataplane()
}

func (ovn *Handler) TransitionToGateway() error {
	ovn.mutex.Lock()
	defer ovn.mutex.Unlock()

	ovn.isGateway = true

	return ovn.updateGatewayDataplane()
}
