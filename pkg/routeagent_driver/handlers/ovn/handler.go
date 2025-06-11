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
	"context"
	"net"
	"sync"

	libovsdbclient "github.com/ovn-org/libovsdb/client"
	"github.com/ovn-org/libovsdb/model"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/watcher"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cidr"
	clientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/netlink"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	k8snet "k8s.io/utils/net"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type NewOVSDBClientFn func(_ model.ClientDBModel, _ ...libovsdbclient.Option) (libovsdbclient.Client, error)

type HandlerConfig struct {
	Namespace       string
	ClusterCIDR     []string
	ServiceCIDR     []string
	SubmClient      clientset.Interface
	K8sClient       kubernetes.Interface
	DynClient       dynamic.Interface
	WatcherConfig   *watcher.Config
	NewOVSDBClient  NewOVSDBClientFn
	TransitSwitchIP TransitSwitchIP
}

type Handler struct {
	event.HandlerBase
	HandlerConfig
	mutex                     sync.Mutex
	cableRoutingInterface     netlink.NetworkInterface
	netLink                   netlink.Interface
	pFilter                   packetfilter.Interface
	gatewayRouteController    *GatewayRouteController
	nonGatewayRouteController *NonGatewayRouteController
	stopCh                    chan struct{}
	ipFamily                  k8snet.IPFamily
}

var logger = log.Logger{Logger: logf.Log.WithName("OVN")}

func NewHandler(ipFamily k8snet.IPFamily, config *HandlerConfig) *Handler {
	// We'll panic if env is nil, this is intentional
	pFilter, err := packetfilter.New(ipFamily)
	if err != nil {
		logger.Fatalf("Error initializing packetfilter in OVN routeagent handler: %s", err)
	}

	h := &Handler{
		HandlerConfig: *config,
		netLink:       netlink.New(),
		pFilter:       pFilter,
		stopCh:        make(chan struct{}),
		ipFamily:      ipFamily,
	}

	if h.NewOVSDBClient == nil {
		h.NewOVSDBClient = libovsdbclient.NewOVSDBClient
	}

	return h
}

func (ovn *Handler) GetName() string {
	return "ovn-hostroutes-handler"
}

func (ovn *Handler) GetNetworkPlugins() []string {
	return []string{cni.OVNKubernetes}
}

func (ovn *Handler) Init(ctx context.Context) error {
	ovn.LegacyCleanup()

	err := ovn.initIPtablesChains()
	if err != nil {
		return err
	}

	ovn.startRouteConfigSyncer(ovn.stopCh)

	connectionHandler := NewConnectionHandler(ovn.ipFamily, ovn.K8sClient, ovn.DynClient)

	err = connectionHandler.initClients(ctx, ovn.NewOVSDBClient)
	if err != nil {
		return errors.Wrapf(err, "error getting connection handler to connect to OvnDB")
	}

	err = ovn.TransitSwitchIP.Init(ctx, ovn.K8sClient)
	if err != nil {
		return errors.Wrap(err, "error initializing TransitSwitchIP")
	}

	gatewayRouteController, err := NewGatewayRouteController(ovn.ipFamily, *ovn.WatcherConfig, connectionHandler, ovn.Namespace)
	if err != nil {
		return err
	}

	ovn.gatewayRouteController = gatewayRouteController

	if err != nil {
		return err
	}

	nonGatewayRouteController, err := NewNonGatewayRouteController(*ovn.WatcherConfig, connectionHandler, ovn.Namespace, ovn.TransitSwitchIP)
	if err != nil {
		return err
	}

	ovn.nonGatewayRouteController = nonGatewayRouteController

	return err
}

func (ovn *Handler) LocalEndpointCreated(endpoint *submV1.Endpoint) error {
	var routingInterface netlink.NetworkInterface
	var err error

	interfaceName := endpoint.Spec.BackendConfig[cable.InterfaceNameConfig]
	if interfaceName != "" {
		// NOTE: This assumes that LocalEndpointCreated happens before than TransitionToGatewayNode
		intf, err := net.InterfaceByName(interfaceName)
		if err != nil {
			return errors.Wrapf(err, "error getting local endpoint interface %q", interfaceName)
		}

		routingInterface = &netlink.DefaultNetworkInterface{Interface: *intf}
	} else {
		if routingInterface, err = ovn.netLink.GetDefaultGatewayInterface(ovn.ipFamily); err != nil {
			logger.Fatalf("Unable to find the default interface on host: %s", err.Error())
		}
	}

	ovn.cableRoutingInterface = routingInterface

	return nil
}

func (ovn *Handler) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	if err := cidr.OverlappingSubnets(ovn.ServiceCIDR, ovn.ClusterCIDR,
		endpoint.Spec.Subnets); err != nil {
		// Skip processing the endpoint when CIDRs overlap and return nil to avoid re-queuing.
		logger.Errorf(err, "overlappingSubnets for new remote %#v returned error", endpoint)
		return nil
	}

	err := ovn.updateHostNetworkDataplane()
	if err != nil {
		return err
	}

	if ovn.State().IsOnGateway() {
		if err = ovn.processEndpointSubnets(true, *endpoint); err != nil {
			return errors.Wrapf(err, "error processing created Endpoint %s", resource.ToJSON(endpoint))
		}

		return ovn.updateGatewayDataplane()
	}

	return nil
}

func (ovn *Handler) RemoteEndpointUpdated(endpoint *submV1.Endpoint) error {
	if err := cidr.OverlappingSubnets(ovn.ServiceCIDR, ovn.ClusterCIDR, endpoint.Spec.Subnets); err != nil {
		// Skip processing the endpoint when CIDRs overlap and return nil to avoid re-queuing.
		logger.Errorf(err, "overlappingSubnets for new remote %#v returned error", endpoint)
		return nil
	}

	err := ovn.updateHostNetworkDataplane()
	if err != nil {
		return err
	}

	if ovn.State().IsOnGateway() {
		return ovn.updateGatewayDataplane()
	}

	return nil
}

func (ovn *Handler) RemoteEndpointRemoved(endpoint *submV1.Endpoint) error {
	err := ovn.updateHostNetworkDataplane()
	if err != nil {
		return err
	}

	if ovn.State().IsOnGateway() {
		if err = ovn.processEndpointSubnets(false, *endpoint); err != nil {
			return errors.Wrapf(err, "error processing removed Endpoint %s", resource.ToJSON(endpoint))
		}

		return ovn.updateGatewayDataplane()
	}

	return nil
}

func (ovn *Handler) TransitionToNonGateway() error {
	if err := ovn.processEndpointSubnets(false, ovn.State().GetRemoteEndpoints()...); err != nil {
		return errors.Wrapf(err, "error processing Endpoints on non-gateway transiton")
	}

	return ovn.cleanupGatewayDataplane()
}

func (ovn *Handler) TransitionToGateway() error {
	if err := ovn.processEndpointSubnets(true, ovn.State().GetRemoteEndpoints()...); err != nil {
		return errors.Wrapf(err, "error processing Endpoints on gateway transiton")
	}

	return ovn.updateGatewayDataplane()
}
