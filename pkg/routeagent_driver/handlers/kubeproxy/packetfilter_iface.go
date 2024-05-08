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
	"strconv"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	"github.com/submariner-io/submariner/pkg/port"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
)

func (kp *SyncHandler) createPFilterChains() error {
	ipHookChains := []packetfilter.ChainIPHook{
		{
			Name:     constants.SmPostRoutingChain,
			Type:     packetfilter.ChainTypeNAT,
			Hook:     packetfilter.ChainHookPostrouting,
			Priority: packetfilter.ChainPriorityFirst,
		},
		{
			Name:     constants.SmInputChain,
			Type:     packetfilter.ChainTypeFilter,
			Hook:     packetfilter.ChainHookInput,
			Priority: packetfilter.ChainPriorityLast,
			JumpRule: &packetfilter.Rule{
				Proto:       packetfilter.RuleProtoUDP,
				Action:      packetfilter.RuleActionJump,
				TargetChain: constants.SmInputChain,
			},
		},
		{
			Name:     constants.SmForwardChain,
			Type:     packetfilter.ChainTypeFilter,
			Hook:     packetfilter.ChainHookForward,
			Priority: packetfilter.ChainPriorityFirst,
		},
	}

	for i := range ipHookChains {
		logger.V(log.DEBUG).Infof("Install/ensure %q/%s IPHook chain exists", ipHookChains[i].Name, "NAT")

		if err := kp.pFilter.CreateIPHookChainIfNotExists(&ipHookChains[i]); err != nil {
			return errors.Wrapf(err, "error installing IPHook chain %q", ipHookChains[i].Name)
		}
	}

	logger.V(log.DEBUG).Infof("Allow VxLAN incoming traffic in %q Chain", constants.SmInputChain)

	ruleSpec := packetfilter.Rule{
		Proto:  packetfilter.RuleProtoUDP,
		DPort:  strconv.Itoa(port.IntraClusterVxLAN),
		Action: packetfilter.RuleActionAccept,
	}

	if err := kp.pFilter.AppendUnique(packetfilter.TableTypeFilter, constants.SmInputChain, &ruleSpec); err != nil {
		return errors.Wrapf(err, "unable to append rule %+v", &ruleSpec)
	}

	logger.V(log.DEBUG).Infof("Insert rule to allow traffic over %s interface in %s Chain", VxLANIface, constants.SmForwardChain)

	ruleSpec = packetfilter.Rule{
		OutInterface: VxLANIface,
		Action:       packetfilter.RuleActionAccept,
	}
	if err := kp.pFilter.PrependUnique(packetfilter.TableTypeFilter, constants.SmForwardChain, &ruleSpec); err != nil {
		return errors.Wrapf(err, "unable to append rule %+v to allow vxlan traffic", &ruleSpec)
	}

	if kp.cniIface != nil {
		// Program rules to support communication from HostNetwork to remoteCluster
		ruleSpec = packetfilter.Rule{
			OutInterface: VxLANIface,
			SrcCIDR:      strconv.Itoa(VxLANVTepNetworkPrefix) + ".0.0.0/8",
			SnatCIDR:     kp.cniIface.IPAddress,
			Action:       packetfilter.RuleActionSNAT,
		}

		logger.V(log.DEBUG).Infof("Installing rule for host network to remote cluster communication: %+v", ruleSpec)

		if err := kp.pFilter.AppendUnique(packetfilter.TableTypeNAT, constants.SmPostRoutingChain, &ruleSpec); err != nil {
			return errors.Wrapf(err, "unable to append rule %+v", &ruleSpec)
		}
	}

	return nil
}

func (kp *SyncHandler) updateIptableRulesForInterClusterTraffic(inputCidrBlocks []string, operation Operation) {
	for _, inputCidrBlock := range inputCidrBlocks {
		err := kp.programIptableRulesForInterClusterTraffic(inputCidrBlock, operation)
		if err != nil {
			logger.Errorf(err, "Failed to program iptable rules")
		}
	}
}

func (kp *SyncHandler) programIptableRulesForInterClusterTraffic(remoteCidrBlock string, operation Operation) error {
	for _, localClusterCidr := range kp.localClusterCidr {
		outboundRule := packetfilter.Rule{
			Action:   packetfilter.RuleActionAccept,
			SrcCIDR:  localClusterCidr,
			DestCIDR: remoteCidrBlock,
		}
		incomingRule := packetfilter.Rule{
			Action:   packetfilter.RuleActionAccept,
			SrcCIDR:  remoteCidrBlock,
			DestCIDR: localClusterCidr,
		}

		if operation == Add {
			logger.V(log.DEBUG).Infof("Installing packetfilter rule for outgoing traffic: %+v", outboundRule)

			if err := kp.pFilter.AppendUnique(packetfilter.TableTypeNAT, constants.SmPostRoutingChain, &outboundRule); err != nil {
				return errors.Wrapf(err, "error appending packetfilter rule %+v", outboundRule)
			}

			logger.V(log.DEBUG).Infof("Installing packetfilter rule for incoming traffic: %+v", incomingRule)

			if err := kp.pFilter.AppendUnique(packetfilter.TableTypeNAT, constants.SmPostRoutingChain, &incomingRule); err != nil {
				return errors.Wrapf(err, "error appending packetfilter rule %+v", incomingRule)
			}
		} else if operation == Delete {
			logger.V(log.DEBUG).Infof("Deleting packetfilter rule for outgoing traffic: %+v", outboundRule)

			if err := kp.pFilter.Delete(packetfilter.TableTypeNAT, constants.SmPostRoutingChain, &outboundRule); err != nil {
				return errors.Wrapf(err, "error deleting packetfilter rule %+v", outboundRule)
			}

			logger.V(log.DEBUG).Infof("Deleting packetfilter rule for incoming traffic: %+v", incomingRule)

			if err := kp.pFilter.Delete(packetfilter.TableTypeNAT, constants.SmPostRoutingChain, &incomingRule); err != nil {
				return errors.Wrapf(err, "error deleting packetfilter rule %+v", incomingRule)
			}
		}
	}

	return nil
}
