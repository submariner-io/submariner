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

package cabledriver

import (
	"errors"

	"github.com/submariner-io/admiral/pkg/log"
	submv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable/vxlan"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/netlink"
	k8snet "k8s.io/utils/net"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type vxlanCleanup struct {
	event.HandlerBase
}

var logger = log.Logger{Logger: logf.Log.WithName("CableDriver")}

func NewVXLANCleanup() event.Handler {
	return &vxlanCleanup{}
}

func (h *vxlanCleanup) GetNetworkPlugins() []string {
	return []string{event.AnyNetworkPlugin}
}

func (h *vxlanCleanup) GetName() string {
	return "VXLAN cleanup handler"
}

func (h *vxlanCleanup) TransitionToNonGateway(localEndpoint *submv1.Endpoint) error {
	// During libreswan to vxlan cable driver update, new vxlan interface is created before old endpoint is deleted.
	// Skip cleanup if removed endpoint wasn't vxlan to avoid deleting the newly created vxlan interface.
	if localEndpoint.Spec.Backend != vxlan.CableDriverName {
		logger.Infof("Skipping VXLAN cleanup - removed endpoint cable driver was %q, not vxlan",
			localEndpoint.Spec.Backend)

		return nil
	}

	logger.Infof("Cleaning up the routes")

	errv6 := netlink.DeleteIfaceAndAssociatedRoutes(vxlan.GetVxlanInterfaceName(k8snet.IPv6), vxlan.TableID, k8snet.IPv6)
	errv4 := netlink.DeleteIfaceAndAssociatedRoutes(vxlan.GetVxlanInterfaceName(k8snet.IPv4), vxlan.TableID, k8snet.IPv4)

	return errors.Join(errv6, errv4)
}
