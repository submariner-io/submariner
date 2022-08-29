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
	"github.com/submariner-io/submariner/pkg/cable/vxlan"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/netlink"
	"k8s.io/klog/v2"
)

type vxlanCleanup struct {
	event.HandlerBase
}

func NewVXLANCleanup() event.Handler {
	return &vxlanCleanup{}
}

func (h *vxlanCleanup) GetNetworkPlugins() []string {
	return []string{event.AnyNetworkPlugin}
}

func (h *vxlanCleanup) GetName() string {
	return "VXLAN cleanup handler"
}

func (h *vxlanCleanup) TransitionToNonGateway() error {
	klog.Infof("Cleaning up the routes")

	return netlink.DeleteIfaceAndAssociatedRoutes(vxlan.VxlanIface, vxlan.TableID) // nolint:wrapcheck  // No need to wrap this error
}
