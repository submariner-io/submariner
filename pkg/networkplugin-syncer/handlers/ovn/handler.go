/*
© 2021 Red Hat, Inc. and others

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

	goovn "github.com/ebay/go-ovn"
	clientset "k8s.io/client-go/kubernetes"

	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/networkplugin-syncer/handlers/ovn/nbctl"
)

var WaitingForLocalEndpoint = errors.New("Waiting for the local endpoint details before we can " +
	"setup any remote endpoint related information, this will be retried.")

type SyncHandler struct {
	event.HandlerBase
	syncMutex             sync.Mutex
	k8sClientset          clientset.Interface
	nbctl                 *nbctl.NbCtl
	nbdb                  goovn.Client
	sbdb                  goovn.Client
	localEndpoint         *submV1.Endpoint
	remoteEndpoints       map[string]*submV1.Endpoint
	submarinerUpstreamIP  string
	submarinerUpstreamNet string
	hostUpstreamIP        string
	hasNodeLocalSwitch    bool
}

func (ovn *SyncHandler) GetName() string {
	return "ovn-sync-handler"
}

func (ovn *SyncHandler) GetNetworkPlugins() []string {
	return []string{"OVNKubernetes"}
}

func NewSyncHandler(k8sClientset clientset.Interface) event.Handler {
	return &SyncHandler{
		remoteEndpoints: make(map[string]*submV1.Endpoint),
		k8sClientset:    k8sClientset,
	}
}

func (ovn *SyncHandler) Init() error {
	if err := ovn.initClients(); err != nil {
		return err
	}

	if err := ovn.detectOvnKubernetesImplementation(); err != nil {
		return err
	}

	if err := ovn.ensureSubmarinerInfra(); err != nil {
		return err
	}

	return nil
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
	ovn.syncMutex.Lock()
	defer ovn.syncMutex.Unlock()

	ovn.remoteEndpoints[endpoint.Name] = endpoint

	return ovn.updateRemoteEndpointsInfra()
}

func (ovn *SyncHandler) RemoteEndpointUpdated(endpoint *submV1.Endpoint) error {
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
		return WaitingForLocalEndpoint // this will be retried eventually
	}

	// Synchronize the policy rules inserted by submariner in the ovn_cluster_router, those point to submariner_router
	err := ovn.setupOvnClusterRouterRemoteRules()
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
