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
	"k8s.io/klog"
)

const (
	submarinerLogicalRouter = "submariner_router"
	ovnClusterRouter        = "ovn_cluster_router"
)

func (ovn *SyncHandler) ensureSubmarinerInfra() error {
	if err := ovn.ensureSubmarinerGatewayLocalNetSwitch(); err != nil {
		return err
	}

	if err := ovn.ensureSubmarinerJoinSwitch(); err != nil {
		return err
	}

	if err := ovn.ensureSubmarinerRouter(); err != nil {
		return err
	}

	if err := ovn.connectOvnClusterRouterToSubm(); err != nil {
		return err
	}

	// At this point, we are missing the ovn_cluster_router policies and the
	// local-to-remote & remote-to-local routes in submariner_router, that
	// depends on endpoint details that we will receive via events.
	return nil
}

// ensureSubmarinerGatewayLocalNetSwitch creates the OVN submariner_join switch which
// connects the ovn_cluster_router to the submariner_router.
func (ovn *SyncHandler) ensureSubmarinerJoinSwitch() error {
	return ovn.ensureSwitch(submarinerDownstreamSwitch)
}

// ensureSubmarinerGatewayLocalNetSwitch creates the OVN submariner_gateway switch and
// virtual ports necessary to make this switch connected to br-submariner on each worker
// node. This switch is used by the submariner gateway to connect to gateway node and remote
// clusters.
func (ovn *SyncHandler) ensureSubmarinerGatewayLocalNetSwitch() error {
	err := ovn.ensureSwitch(submarinerUpstreamSwitch)
	if err != nil {
		return err
	}

	lspCmd, err := ovn.nbdb.LSPAdd(submarinerUpstreamSwitch, submarinerUpstreamLocalnetPort)
	if err == nil {
		err = ovn.nbdb.Execute(lspCmd)
	}

	if err != nil && !errors.Is(err, goovn.ErrorExist) {
		return errors.Wrapf(err, "error creating Submariner localnet port %q", submarinerUpstreamLocalnetPort)
	}

	lspTypeCmd, err := ovn.nbdb.LSPSetType(submarinerUpstreamLocalnetPort, "localnet")
	if err == nil {
		err = ovn.nbdb.Execute(lspTypeCmd)
	}

	if err != nil {
		return errors.Wrapf(err, "error setting Submariner localnet port %q type", submarinerUpstreamLocalnetPort)
	}

	lspSetAddressesCmd, err := ovn.nbdb.LSPSetAddress(submarinerUpstreamLocalnetPort, "unknown")
	if err == nil {
		err = ovn.nbdb.Execute(lspSetAddressesCmd)
	}

	if err != nil {
		return errors.Wrapf(err, "error setting Submariner localnet port %q address", submarinerUpstreamLocalnetPort)
	}

	lspSetOptionsCmd, err := ovn.nbdb.LSPSetOptions(
		submarinerUpstreamLocalnetPort, map[string]string{"network_name": SubmarinerUpstreamLocalnet})

	if err == nil {
		err = ovn.nbdb.Execute(lspSetOptionsCmd)
	}

	if err != nil {
		return errors.Wrapf(err, "error setting Submariner localnet port %q address", submarinerUpstreamLocalnetPort)
	}

	return nil
}

func (ovn *SyncHandler) ensureSwitch(switchName string) error {
	klog.Infof("Ensuring %q switch", switchName)

	lsCmd, err := ovn.nbdb.LSAdd(switchName)
	if err == nil {
		klog.Infof("Creating submariner switch %q", switchName)

		err = ovn.nbdb.Execute(lsCmd)
	}

	if err != nil && !errors.Is(err, goovn.ErrorExist) {
		return errors.Wrapf(err, "error creating submariner switch %q", switchName)
	}

	return nil
}

func (ovn *SyncHandler) ensureSubmarinerRouter() error {
	klog.Infof("Ensuring %q", submarinerLogicalRouter)

	lrCmd, err := ovn.nbdb.LRAdd(submarinerLogicalRouter, nil)
	if err == nil {
		klog.Infof("Creating submariner router %q", submarinerLogicalRouter)

		err = ovn.nbdb.Execute(lrCmd)
	}

	if err != nil && !errors.Is(err, goovn.ErrorExist) {
		return errors.Wrapf(err, "error creating submariner router %q", submarinerLogicalRouter)
	}

	// TODO: Improve this in goovn so we don't need to delete/recreate everytime, LinkSwitchToRouter
	//       does not provide good error handling and ignores many corner cases.
	delOldRouterPort, _ := ovn.nbdb.LRPDel(submarinerLogicalRouter, submarinerDownstreamRPort)
	_ = ovn.nbdb.Execute(delOldRouterPort)
	delOldLSP, _ := ovn.nbdb.LSPDel(submarinerDownstreamSwPort)
	_ = ovn.nbdb.Execute(delOldLSP)

	linkCmd, err := ovn.nbdb.LinkSwitchToRouter(
		submarinerDownstreamSwitch, submarinerDownstreamSwPort,
		submarinerLogicalRouter, submarinerDownstreamRPort,
		submarinerDownstreamMAC,
		[]string{submarinerDownstreamNET}, nil,
	)

	if err != nil && !errors.Is(err, goovn.ErrorExist) {
		return errors.Wrapf(err, "error link for port %q, linkCmd: %#v", submarinerDownstreamRPort, linkCmd)
	}

	err = ovn.nbdb.Execute(linkCmd)
	if err != nil {
		return errors.Wrapf(err, "error creating %q port %q", submarinerLogicalRouter, submarinerDownstreamRPort)
	}

	return nil
}
