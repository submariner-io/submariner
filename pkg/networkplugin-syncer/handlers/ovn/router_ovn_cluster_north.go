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
	// The ovn_cluster_router submariner port connects to the submariner router
	ovnClusterSubmarinerRPort  = "ovn_cluster_subm_lrp"
	ovnClusterSubmarinerSwPort = "ovn_cluster_subm_lsp"
	ovnClusterSubmarinerMAC    = "00:60:2f:10:01:02"
	ovnClusterSubmarinerNET    = ovnClusterSubmarinerIP + "/29"
	ovnClusterSubmarinerIP     = "169.254.254.2"
	ovnRoutePoliciesPrio       = 20000
)

func (ovn *SyncHandler) connectOvnClusterRouterToSubm() error {
	klog.Infof("Ensuring %q is connected to %q", submarinerLogicalRouter, ovnClusterRouter)
	// TODO: Improve this in goovn so we don't need to delete/recreate everytime, LinkSwitchToRouter
	//       does not provide good error handling and ignores many corner cases.
	delOldRouterPort, _ := ovn.nbdb.LRPDel(ovnClusterRouter, ovnClusterSubmarinerRPort)
	_ = ovn.nbdb.Execute(delOldRouterPort)
	delOldLSP, _ := ovn.nbdb.LSPDel(ovnClusterSubmarinerSwPort)
	_ = ovn.nbdb.Execute(delOldLSP)

	linkCmd, err := ovn.nbdb.LinkSwitchToRouter(
		submarinerDownstreamSwitch, ovnClusterSubmarinerSwPort,
		ovnClusterRouter, ovnClusterSubmarinerRPort,
		ovnClusterSubmarinerMAC,
		[]string{ovnClusterSubmarinerNET}, nil,
	)

	if err != nil && !errors.Is(err, goovn.ErrorExist) {
		return errors.Wrapf(err, "creating command to connect ovn_cluster_router")
	}

	err = ovn.nbdb.Execute(linkCmd)
	if err != nil {
		return errors.Wrapf(err, "error creating %q port %q", ovnClusterRouter, ovnClusterSubmarinerRPort)
	}

	return nil
}

func (ovn *SyncHandler) associateSubmarinerRouterToChassis(chassis *goovn.Chassis) error {
	return ovn.nbctl.SetRouterChassis(submarinerLogicalRouter, chassis.Name)
}

func (ovn *SyncHandler) setupOvnClusterRouterRemoteRules() error {
	existingSubnetPolicies, err := ovn.nbctl.LrPolicyGetSubnets(ovnClusterRouter, submarinerDownstreamIP)
	if err != nil {
		return errors.Wrapf(err, "error reading existing routing policies from %q", ovnClusterRouter)
	}

	klog.V(log.DEBUG).Infof("Existing routing policies in %q router for subnets %v", ovnClusterRouter, existingSubnetPolicies.Elements())

	toAdd, toRemove := ovn.getNorthSubnetsToAddAndRemove(existingSubnetPolicies)

	ovn.logRoutingChanges("north policies", ovnClusterRouter, toAdd, toRemove)

	err = ovn.addPoliciesForRemoteSubnets(toAdd)
	if err != nil {
		return err
	}

	err = ovn.removePoliciesForRemoteSubnets(toRemove)
	if err != nil {
		return err
	}

	return nil
}

func (ovn *SyncHandler) removePoliciesForRemoteSubnets(toRemove []string) error {
	for _, subnet := range toRemove {
		err := ovn.nbctl.LrPolicyDel(ovnClusterRouter, ovnRoutePoliciesPrio, "ip4.dst == "+subnet)
		if err != nil {
			return errors.Wrapf(err, "error removing the %q routing rule to router %q", subnet, ovnClusterRouter)
		}
	}

	return nil
}

func (ovn *SyncHandler) addPoliciesForRemoteSubnets(toAdd []string) error {
	for _, subnet := range toAdd {
		err := ovn.nbctl.LrPolicyAdd(ovnClusterRouter, ovnRoutePoliciesPrio, "ip4.dst == "+subnet, "reroute", submarinerDownstreamIP)
		if err != nil {
			return errors.Wrapf(err, "error adding the %q routing rule to router %q", subnet, ovnClusterRouter)
		}
	}

	return nil
}
