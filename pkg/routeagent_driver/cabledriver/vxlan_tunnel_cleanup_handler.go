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
package cleanup

import (
	"fmt"
	"syscall"

	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/cable/vxlan"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/cleanup"
	"github.com/vishvananda/netlink"
	"k8s.io/klog"
)

func GetVXLANCleanupHandlers() []cleanup.Handler {
	return []cleanup.Handler{
		NewVXLANTunnelCleanupHandler(),
	}
}

type VXLANTunnelCleanupHandler struct{}

func NewVXLANTunnelCleanupHandler() cleanup.Handler {
	return &VXLANTunnelCleanupHandler{}
}

func (xc *VXLANTunnelCleanupHandler) GetName() string {
	return "VXLAN cleanup handler"
}

func (xc *VXLANTunnelCleanupHandler) NonGatewayCleanup() error {
	klog.V(log.DEBUG).Infof("Cleaning up the routes")

	link, err := netlink.LinkByName(vxlan.VxLANIface)

	if err != nil {
		klog.Errorf("Unable to cleanup routes, error retrieving the link %s: %v", vxlan.VxLANIface, err)
		return nil
	}

	currentRouteList, err := netlink.RouteList(link, syscall.AF_INET)

	if err != nil {
		klog.Errorf("Unable to cleanup routes, error retrieving routes on the link %s: %v", vxlan.VxLANIface, err)
		return nil
	}

	for i := range currentRouteList {
		klog.V(log.DEBUG).Infof("Processing route %v", currentRouteList[i])

		if err = netlink.RouteDel(&currentRouteList[i]); err != nil {
			klog.Errorf("Error removing route %s: %v", currentRouteList[i], err)
		}
	}

	err = netlink.LinkDel(link)
	if err != nil {
		return fmt.Errorf("failed to delete the the vxlan interface: %v", err)
	}

	return nil
}

func (xc *VXLANTunnelCleanupHandler) GatewayToNonGatewayTransition() error {
	return nil
}
