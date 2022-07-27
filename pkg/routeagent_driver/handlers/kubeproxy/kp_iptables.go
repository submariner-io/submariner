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

package kubeproxy

import (
	"net"
	"os"
	"sync"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/stringset"
	cni "github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/netlink"
	cniapi "github.com/submariner-io/submariner/pkg/routeagent_driver/cni"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
)

type SyncHandler struct {
	event.HandlerBase
	localCableDriver string
	localClusterCidr []string
	localServiceCidr []string

	remoteSubnets           stringset.Interface
	remoteSubnetGw          map[string]net.IP
	remoteVTEPs             stringset.Interface
	routeCacheGWNode        stringset.Interface
	remoteEndpointTimeStamp map[string]v1.Time

	syncHandlerMutex     sync.Mutex
	isGatewayNode        bool
	wasGatewayPreviously bool

	netLink          netlink.Interface
	vxlanDevice      *vxLanIface
	vxlanGwIP        *net.IP
	hostname         string
	cniIface         *cniapi.Interface
	defaultHostIface *net.Interface
}

func NewSyncHandler(localClusterCidr, localServiceCidr []string) *SyncHandler {
	return &SyncHandler{
		localClusterCidr:        localClusterCidr,
		localServiceCidr:        localServiceCidr,
		localCableDriver:        "",
		remoteSubnets:           stringset.NewSynchronized(),
		remoteSubnetGw:          map[string]net.IP{},
		remoteEndpointTimeStamp: map[string]v1.Time{},
		remoteVTEPs:             stringset.NewSynchronized(),
		routeCacheGWNode:        stringset.NewSynchronized(),
		isGatewayNode:           false,
		wasGatewayPreviously:    false,
		netLink:                 netlink.New(),
	}
}

func (kp *SyncHandler) GetName() string {
	return "kubeproxy-iptables-handler"
}

func (kp *SyncHandler) GetNetworkPlugins() []string {
	return []string{
		cni.Generic, cni.CanalFlannel, cni.Flannel,
		cni.WeaveNet, cni.OpenShiftSDN, cni.Calico,
	}
}

func (kp *SyncHandler) Init() error {
	var err error

	kp.hostname, err = os.Hostname()
	if err != nil {
		return errors.Wrapf(err, "unable to determine hostname")
	}

	kp.defaultHostIface, err = netlink.GetDefaultGatewayInterface()
	if err != nil {
		return errors.Wrapf(err, "Unable to find the default interface on host: %s", kp.hostname)
	}

	cniIface, err := cniapi.Discover(kp.localClusterCidr[0])
	if err == nil {
		// Configure CNI Specific changes
		kp.cniIface = cniIface

		err := kp.netLink.EnableLooseModeReversePathFilter(kp.cniIface.Name)
		if err != nil {
			return errors.Wrap(err, "error enabling loose mode")
		}
	} else {
		// This is not a fatal error. Connectivity and other datapath use-cases will continue
		// to work, but the following use-cases may not work.
		// 1. Hostnetworking to remote cluster support will be broken
		// 2. Health-check verification between the Gateway nodes will be disabled
		klog.Errorf("Error discovering the CNI interface %v", err)
	}

	// Create the necessary IPTable chains in the filter and nat tables.
	err = kp.createIPTableChains()
	if err != nil {
		return errors.Wrapf(err, "createIPTableChains returned error")
	}

	return nil
}
