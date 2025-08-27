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
	"context"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/util"
	nodeutil "github.com/submariner-io/submariner/pkg/node"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/ovn/vsctl"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8snet "k8s.io/utils/net"
)

func (ovn *Handler) Stop() error {
	ovn.gatewayRouteController.stop()

	if ovn.nonGatewayRouteController != nil {
		ovn.nonGatewayRouteController.stop()
	}

	close(ovn.stopCh)

	return nil
}

func (ovn *Handler) Uninstall() error {
	logger.Infof("Uninstalling OVN components from the node")

	err := ovn.cleanupRoutes()
	if err != nil {
		logger.Errorf(err, "Error cleaning the routes")
	}

	err = ovn.netLink.FlushRouteTable(constants.RouteAgentInterClusterNetworkTableID)
	if err != nil {
		logger.Errorf(err, "Flushing routing table %d returned error",
			constants.RouteAgentInterClusterNetworkTableID)
	}

	err = ovn.netLink.FlushRouteTable(constants.RouteAgentHostNetworkTableID)
	if err != nil {
		logger.Errorf(err, "Flushing routing table %d returned error",
			constants.RouteAgentHostNetworkTableID)
	}

	ovn.deleteIPHookChain(packetfilter.TableTypeFilter, newSubmarinerFWDChain())
	ovn.deleteIPHookChain(packetfilter.TableTypeFilter, newSubmarinerMSSClampChain())
	ovn.deleteIPHookChain(packetfilter.TableTypeNAT, newPostRoutingChain())

	err = util.Update[*corev1.Node](context.TODO(), ovn.nodeResourceInterface(), &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: nodeutil.GetLocalNodeName()},
	}, func(existing *corev1.Node) (*corev1.Node, error) {
		delete(existing.Annotations, OVNKSNATExcludeSubnetsAnnotation)
		return existing, nil
	})
	if err != nil {
		logger.Errorf(err, "Error removing node annotation %q", OVNKSNATExcludeSubnetsAnnotation)
	}

	return nil
}

func (ovn *Handler) deleteIPHookChain(table packetfilter.TableType, chain *packetfilter.ChainIPHook) {
	if err := ovn.pFilter.ClearChain(table, chain.Name); err != nil {
		logger.Errorf(err, "Error clearing IP hook chain %q from %q table", chain.Name, table)
	}

	if err := ovn.pFilter.DeleteIPHookChain(chain); err != nil {
		logger.Errorf(err, "Error deleting IP hook chain %q from %q table", chain.Name, table)
	}
}

func (ovn *Handler) cleanupRoutes() error {
	rules, err := ovn.netLink.RuleList(k8snet.IPv4)
	if err != nil {
		return errors.Wrapf(err, "error listing rules")
	}

	for i := range rules {
		if rules[i].Table == constants.RouteAgentInterClusterNetworkTableID || rules[i].Table == constants.RouteAgentHostNetworkTableID {
			err = ovn.netLink.RuleDel(&rules[i])
			if err != nil {
				return errors.Wrapf(err, "error deleting the rule %v", rules[i])
			}
		}
	}

	return nil
}

func (ovn *Handler) LegacyCleanup() {
	err := vsctl.DelInternalPort(ovnK8sSubmarinerBridge, ovnK8sSubmarinerInterface)
	if err != nil {
		logger.Errorf(err, "Error deleting Submariner port %q", ovnK8sSubmarinerInterface)
	}

	err = vsctl.DelBridge(ovnK8sSubmarinerBridge)
	if err != nil {
		logger.Errorf(err, "Error deleting Submariner bridge %q", ovnK8sSubmarinerBridge)
	}
}
