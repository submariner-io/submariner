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
	"strings"

	goovn "github.com/ebay/go-ovn"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"k8s.io/klog"
)

const (
	// The downstream port connects to ovn_cluster_router, allowing connectivity to pods and services
	submarinerDownstreamSwitch = "submariner_join"
	submarinerDownstreamRPort  = "submariner_j_lrp"
	submarinerDownstreamSwPort = "submariner_j_lsp"
	submarinerDownstreamMAC    = "00:60:2f:10:01:03"
	submarinerDownstreamNET    = submarinerDownstreamIP + "/29"
	submarinerDownstreamIP     = "169.254.254.1"
)

func (ovn *SyncHandler) updateGatewayNode() error {
	if ovn.localEndpoint == nil {
		klog.Warningf("No local endpoint, cannot update local endpoint information in OVN NBDB")
		return nil
	}

	gwHostname := ovn.localEndpoint.Spec.Hostname
	chassis, err := ovn.findChassisByHostname(gwHostname)

	if errors.Is(err, goovn.ErrorNotFound) {
		klog.Fatalf("The OVN chassis for hostname %q could not be found", gwHostname)
	} else if err != nil {
		// Hopefully this error can be retried
		return err
	}

	klog.V(log.DEBUG).Infof("Chassis for gw %q is %q, host: %q", gwHostname, chassis.Name, chassis.Hostname)

	var submExternalPortSwitch string
	if ovn.hasNodeLocalSwitch {
		submExternalPortSwitch = nodeLocalSwitch
	} else {
		submExternalPortSwitch = "ext_" + chassis.Hostname
	}

	// Create/update the submariner external port associated to one of the external switches
	if err := ovn.createOrUpdateSubmarinerExternalPort(submExternalPortSwitch); err != nil && !errors.Is(err, goovn.ErrorExist) {
		return err
	}

	// Update ovn-kubernetes host management ACL to allow return of fragmentation ICMP for PMTU discovery
	if err := ovn.changeMgmtAllowRelatedACL(chassis.Hostname); err != nil {
		return err
	}

	// Associate the port to an specific chassis (=host) on OVN so the traffic flows out/in through that host
	// the active submariner-gateway in our case
	if err := ovn.associateSubmarinerRouterToChassis(chassis); err != nil {
		return err
	}

	// Associate the current chassis k8s load balancers to the submariner_router
	if err := ovn.ensureServiceLoadBalancersFrom(chassis.Hostname); err != nil {
		return err
	}

	return ovn.updateSubmarinerRouterLocalRoutes()
}

func (ovn *SyncHandler) findChassisByHostname(hostname string) (*goovn.Chassis, error) {
	chassisList, err := ovn.sbdb.ChassisList()
	if err != nil {
		return nil, errors.Wrap(err, "unable to get chassis list from OVN")
	}
	for _, chassis := range chassisList {
		if chassis.Hostname == hostname || strings.HasPrefix(chassis.Hostname, hostname) ||
			strings.HasPrefix(hostname, chassis.Hostname) {
			return chassis, nil
		}
	}

	return nil, goovn.ErrorNotFound
}

func (ovn *SyncHandler) updateSubmarinerRouterLocalRoutes() error {
	existingRoutes, err := ovn.getExistingSubmarinerRouterRoutesToPort(submarinerDownstreamRPort)
	if err != nil {
		return err
	}

	klog.V(log.DEBUG).Infof("Existing south routes in %q router for subnets %v", submarinerLogicalRouter, existingRoutes.Elements())

	toAdd, toRemove := ovn.getSouthSubnetsToAddAndRemove(existingRoutes)

	ovn.logRoutingChanges("south routes", submarinerLogicalRouter, toAdd, toRemove)

	ovnCommands, err := ovn.addSubmRoutesToSubnets(toAdd, submarinerDownstreamRPort, ovnClusterSubmarinerIP, []*goovn.OvnCommand{})
	if err != nil {
		return err
	}

	ovnCommands, err = ovn.removeRoutesToSubnets(toRemove, submarinerDownstreamRPort, ovnCommands)
	if err != nil {
		return err
	}

	err = ovn.nbdb.Execute(ovnCommands...)
	if err != nil {
		return errors.Wrapf(err, "error executing routing rule modifications for router %q", submarinerLogicalRouter)
	}

	return nil
}
