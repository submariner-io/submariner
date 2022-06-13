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
	"errors"
	"sync"

	libovsdbclient "github.com/ovn-org/libovsdb/client"
	"github.com/submariner-io/admiral/pkg/log"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cidr"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/environment"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog"
)

var ErrWaitingForLocalEndpoint = errors.New("waiting for the local endpoint details before we can " +
	"setup any remote endpoint related information, this will be retried")

type SyncHandler struct {
	event.HandlerBase
	syncMutex        sync.Mutex
	k8sClientset     clientset.Interface
	nbdb             libovsdbclient.Client
	sbdb             libovsdbclient.Client
	localClusterCIDR []string
	localServiceCIDR []string
	localEndpoint    *submV1.Endpoint
	remoteEndpoints  map[string]*submV1.Endpoint
}

func (ovn *SyncHandler) GetName() string {
	return "ovn-sync-handler"
}

func (ovn *SyncHandler) GetNetworkPlugins() []string {
	return []string{cni.OVNKubernetes}
}

func NewSyncHandler(k8sClientset clientset.Interface, env *environment.Specification) event.Handler {
	// We'll panic if env is nil, this is intentional
	return &SyncHandler{
		remoteEndpoints:  make(map[string]*submV1.Endpoint),
		k8sClientset:     k8sClientset,
		localClusterCIDR: env.ClusterCidr,
		localServiceCIDR: env.ServiceCidr,
	}
}

func (ovn *SyncHandler) Init() error {
	if err := ovn.initClients(); err != nil {
		return err
	}

	return ovn.ensureSubmarinerInfra()
}

func (ovn *SyncHandler) LocalEndpointCreated(endpoint *submV1.Endpoint) error {
	ovn.syncMutex.Lock()
	defer ovn.syncMutex.Unlock()

	ovn.localEndpoint = endpoint

	return ovn.updateGatewayNode()
}

func (ovn *SyncHandler) LocalEndpointUpdated(endpoint *submV1.Endpoint) error {
	ovn.syncMutex.Lock()
	defer ovn.syncMutex.Unlock()

	ovn.localEndpoint = endpoint

	return ovn.updateGatewayNode()
}

func (ovn *SyncHandler) LocalEndpointRemoved(endpoint *submV1.Endpoint) error {
	ovn.syncMutex.Lock()
	defer ovn.syncMutex.Unlock()

	if ovn.localEndpoint.Name == endpoint.Name {
		ovn.localEndpoint = nil
	}

	return nil
}

func (ovn *SyncHandler) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	if err := cidr.OverlappingSubnets(ovn.localServiceCIDR, ovn.localClusterCIDR, endpoint.Spec.Subnets); err != nil {
		// Skip processing the endpoint when CIDRs overlap and return nil to avoid re-queuing.
		klog.Errorf("overlappingSubnets for new remote %#v returned error: %v", endpoint, err)
		return nil
	}

	ovn.syncMutex.Lock()
	defer ovn.syncMutex.Unlock()

	ovn.remoteEndpoints[endpoint.Name] = endpoint

	return ovn.updateRemoteEndpointsInfra()
}

func (ovn *SyncHandler) RemoteEndpointUpdated(endpoint *submV1.Endpoint) error {
	if err := cidr.OverlappingSubnets(ovn.localServiceCIDR, ovn.localClusterCIDR, endpoint.Spec.Subnets); err != nil {
		// Skip processing the endpoint when CIDRs overlap and return nil to avoid re-queuing.
		klog.Errorf("overlappingSubnets for new remote %#v returned error: %v", endpoint, err)
		return nil
	}

	ovn.syncMutex.Lock()
	defer ovn.syncMutex.Unlock()

	ovn.remoteEndpoints[endpoint.Name] = endpoint

	return ovn.updateRemoteEndpointsInfra()
}

func (ovn *SyncHandler) RemoteEndpointRemoved(endpoint *submV1.Endpoint) error {
	ovn.syncMutex.Lock()
	defer ovn.syncMutex.Unlock()

	delete(ovn.remoteEndpoints, endpoint.Name)

	return ovn.updateRemoteEndpointsInfra()
}

func (ovn *SyncHandler) updateRemoteEndpointsInfra() error {
	if ovn.localEndpoint == nil {
		// If we don't have information on the localEndpoint chances are that we are not detecting
		// the local endpoint yet (right CLUSTER_ID set), with the risk of setting up local routes as
		// remote routes and breaking the cluster.
		return ErrWaitingForLocalEndpoint // this will be retried eventually
	}

	// Synchronize the policy rules inserted by submariner in the ovn_cluster_router, those point to submariner_router
	err := ovn.setupOvnClusterRouterLRPs()
	if err != nil {
		return err
	}

	// Synchronize the routing rules inserted into submariner_router pointing to the remote clusters via the node IP in
	// the ovs external network bridge used by OVN kubernetes to talk to the host.
	err = ovn.updateSubmarinerRouterRemoteRoutes()
	if err != nil {
		return err
	}

	return nil
}

func (ovn *SyncHandler) updateGatewayNode() error {
	if ovn.localEndpoint == nil {
		klog.Warningf("No local endpoint, cannot update local endpoint information in OVN NBDB")
		return nil
	}

	gwHostname := ovn.localEndpoint.Spec.Hostname

	chassis, err := ovn.findChassisByHostname(gwHostname)
	if errors.Is(err, libovsdbclient.ErrNotFound) {
		klog.Fatalf("The OVN chassis for hostname %q could not be found", gwHostname)
	} else if err != nil {
		// Hopefully this error can be retried
		return err
	}

	klog.V(log.DEBUG).Infof("Chassis for gw %q is %q, host: %q", gwHostname, chassis.Name, chassis.Hostname)

	// Create/update the submariner external port associated to one of the external switches.
	if err := ovn.createOrUpdateSubmarinerExternalPort(); err != nil {
		return err
	}

	// Associate the port to an specific chassis (=host) on OVN so the traffic flows out/in through that host
	// the active submariner-gateway in our case.
	if err := ovn.associateSubmarinerRouterToChassis(chassis); err != nil {
		return err
	}

	return ovn.updateSubmarinerRouterLocalRoutes()
}
