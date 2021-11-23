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
	goovn "github.com/ebay/go-ovn"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"k8s.io/klog"
)

const (
	// The upstream port connects to the host on the GW, right before/after the encryption cable driver.
	submarinerUpstreamSwPort       = "submariner_up_lsp"
	submarinerUpstreamRPort        = "submariner_up_lrp"
	submarinerUpstreamMAC          = "00:60:2f:10:01:01"
	submarinerUpstreamNET          = SubmarinerUpstreamIP + "/29"
	SubmarinerUpstreamIP           = "169.254.254.9"  // public constant, used in the route-agent handler
	HostUpstreamIP                 = "169.254.254.10" // we configure this one as ovn-k8s-sub0 interface on each worker node
	HostUpstreamNET                = HostUpstreamIP + "/29"
	SubmarinerUpstreamLocalnet     = "submariner_gateway"
	submarinerUpstreamSwitch       = "submariner_gateway" // this switch is mapped as br-submariner on each worker node
	submarinerUpstreamLocalnetPort = "submariner_localnet"
)

func (ovn *SyncHandler) createOrUpdateSubmarinerExternalPort() error {
	// TODO: Improve this so we don't need to delete/recreate everytime
	delOldRouterPort, _ := ovn.nbdb.LRPDel(submarinerLogicalRouter, submarinerUpstreamRPort)
	_ = ovn.nbdb.Execute(delOldRouterPort)
	delOldLSP, _ := ovn.nbdb.LSPDel(submarinerUpstreamSwPort)
	_ = ovn.nbdb.Execute(delOldLSP)

	linkCmd, err := ovn.nbdb.LinkSwitchToRouter(
		submarinerUpstreamSwitch, submarinerUpstreamSwPort,
		submarinerLogicalRouter, submarinerUpstreamRPort,
		submarinerUpstreamMAC,
		[]string{submarinerUpstreamNET}, nil,
	)

	if err == nil {
		klog.Infof("Creating submariner upstream port %q", submarinerUpstreamRPort)

		err = ovn.nbdb.Execute(linkCmd)
		if err != nil {
			return errors.Wrapf(err, "error creating the submariner upstream port %q", submarinerUpstreamRPort)
		}
	} else {
		return errors.Wrapf(err, "error creating the submariner upstream port command %q", submarinerUpstreamRPort)
	}

	return nil
}

func (ovn *SyncHandler) updateSubmarinerRouterRemoteRoutes() error {
	existingRoutes, err := ovn.getExistingSubmarinerRouterRoutesToPort(submarinerUpstreamRPort)
	if err != nil {
		return err
	}

	klog.V(log.DEBUG).Infof("Existing north routes in %q router for subnets %v", submarinerLogicalRouter, existingRoutes.Elements())

	toAdd, toRemove := ovn.getNorthSubnetsToAddAndRemove(existingRoutes)

	ovn.logRoutingChanges("north routes", submarinerLogicalRouter, toAdd, toRemove)

	ovnCommands, err := ovn.addSubmRoutesToSubnets(toAdd, submarinerUpstreamRPort, HostUpstreamIP, []*goovn.OvnCommand{})
	if err != nil {
		return err
	}

	ovnCommands, err = ovn.removeRoutesToSubnets(toRemove, submarinerUpstreamRPort, ovnCommands)
	if err != nil {
		return err
	}

	err = ovn.nbdb.Execute(ovnCommands...)
	if err != nil {
		return errors.Wrapf(err, "error executing routing rule modifications for router %q", submarinerLogicalRouter)
	}

	return nil
}

func (ovn *SyncHandler) removeRoutesToSubnets(toRemove []string, viaPort string, ovnCommands []*goovn.OvnCommand) ([]*goovn.OvnCommand,
	error) {
	for _, subnet := range toRemove {
		delCmd, err := ovn.nbdb.LRSRDel(submarinerLogicalRouter, subnet, nil, &viaPort, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "error creating LRSRDel for router %q", submarinerLogicalRouter)
		}

		ovnCommands = append(ovnCommands, delCmd)
	}

	return ovnCommands, nil
}
