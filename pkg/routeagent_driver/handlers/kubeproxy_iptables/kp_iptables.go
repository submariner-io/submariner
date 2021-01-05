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
package kubeproxy_iptables

import (
	"net"
	"os"
	"sync"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/stringset"
	"k8s.io/klog"

	cableCleanup "github.com/submariner-io/submariner/pkg/cable/cleanup"
	clientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/cleanup"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/cni_interface"
	"github.com/submariner-io/submariner/pkg/util"
)

type SyncHandler struct {
	event.HandlerBase
	localCableDriver string
	localClusterCidr []string
	localServiceCidr []string

	remoteSubnets    stringset.Interface
	remoteVTEPs      stringset.Interface
	routeCacheGWNode stringset.Interface

	syncHandlerMutex     sync.Mutex
	isGatewayNode        bool
	wasGatewayPreviously bool

	vxlanDevice      *vxLanIface
	vxlanGwIP        *net.IP
	hostname         string
	cniIface         *cni_interface.Interface
	defaultHostIface *net.Interface

	smClientSet     clientset.Interface
	cleanupHandlers []cleanup.Handler
}

func NewSyncHandler(localClusterCidr, localServiceCidr []string, smClientSet clientset.Interface) *SyncHandler {
	return &SyncHandler{
		localClusterCidr:     localClusterCidr,
		localServiceCidr:     localServiceCidr,
		localCableDriver:     "",
		remoteSubnets:        stringset.NewSynchronized(),
		remoteVTEPs:          stringset.NewSynchronized(),
		routeCacheGWNode:     stringset.NewSynchronized(),
		isGatewayNode:        false,
		wasGatewayPreviously: false,
		vxlanDevice:          nil,
		vxlanGwIP:            nil,
		smClientSet:          smClientSet,
	}
}

func (kp *SyncHandler) GetName() string {
	return "kubeproxy-iptables-handler"
}

func (kp *SyncHandler) GetNetworkPlugins() []string {
	return []string{"generic", "canal-flannel", "weave-net", "OpenShiftSDN"}
}

func (kp *SyncHandler) Init() error {
	var err error
	kp.hostname, err = os.Hostname()
	if err != nil {
		return errors.Wrapf(err, "unable to determine hostname")
	}

	kp.defaultHostIface, err = util.GetDefaultGatewayInterface()
	if err != nil {
		return errors.Wrapf(err, "Unable to find the default interface on host: %s", kp.hostname)
	}

	cniIface, err := cni_interface.Discover(kp.localClusterCidr[0])
	if err == nil {
		// Configure CNI Specific changes
		kp.cniIface = cniIface
		err := cni_interface.ConfigureRpFilter(kp.cniIface.Name)
		if err != nil {
			return errors.Wrapf(err, "ConfigureRpFilter returned error")
		}
	} else {
		// This is not a fatal error. Hostnetworking to remote cluster support will be broken
		// but other use-cases can continue to work.
		klog.Errorf("Error discovering the CNI interface %v", err)
	}

	// Create the necessary IPTable chains in the filter and nat tables.
	err = kp.createIPTableChains()
	if err != nil {
		return errors.Wrapf(err, "createIPTableChains returned error")
	}

	// For now we get all the cleanups
	kp.installCleanupHandlers(cableCleanup.GetCleanupHandlers())

	return nil
}
