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
	"fmt"
	"syscall"

	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/cable/vxlan"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/vishvananda/netlink"
	"k8s.io/klog"
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

	link, err := netlink.LinkByName(vxlan.VxlanIface)

	if err != nil {
		return nil
	}

	currentRouteList, err := netlink.RouteList(link, syscall.AF_INET)

	if err != nil {
		klog.Warningf("Unable to cleanup routes, error retrieving routes on the link %s: %v", vxlan.VxlanIface, err)
	} else {
		for i := range currentRouteList {
			klog.V(log.DEBUG).Infof("Processing route %v", currentRouteList[i])
			if currentRouteList[i].Table == vxlan.TableID {
				if err = netlink.RouteDel(&currentRouteList[i]); err != nil {
					klog.Errorf("Error removing route %s: %v", currentRouteList[i], err)
				}
			}
		}
	}

	err = netlink.LinkDel(link)
	if err != nil {
		return fmt.Errorf("failed to delete the the vxlan interface: %v", err)
	}

	return nil
}
