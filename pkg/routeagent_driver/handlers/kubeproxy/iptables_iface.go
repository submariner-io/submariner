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
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/iptables"
	"github.com/submariner-io/submariner/pkg/port"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	iptcommon "github.com/submariner-io/submariner/pkg/routeagent_driver/iptables"
)

func (kp *SyncHandler) createIPTableChains() error {
	ipt, err := iptables.New()
	if err != nil {
		return errors.Wrap(err, "error initializing iptables")
	}

	if err := iptcommon.InitSubmarinerPostRoutingChain(ipt); err != nil {
		return errors.Wrap(err, "error initializing POST routing chain")
	}

	logger.V(log.DEBUG).Infof("Install/ensure %q chain exists", constants.SmInputChain)

	if err = ipt.CreateChainIfNotExists(constants.FilterTable, constants.SmInputChain); err != nil {
		return errors.Wrap(err, "unable to create SUBMARINER-INPUT chain in iptables")
	}

	forwardToSubInputRuleSpec := []string{"-p", "udp", "-m", "udp", "-j", constants.SmInputChain}
	if err = ipt.AppendUnique(constants.FilterTable, constants.InputChain, forwardToSubInputRuleSpec...); err != nil {
		return errors.Wrapf(err, "unable to append iptables rule %q", strings.Join(forwardToSubInputRuleSpec, " "))
	}

	logger.V(log.DEBUG).Infof("Allow VxLAN incoming traffic in %q Chain", constants.SmInputChain)

	ruleSpec := []string{"-p", "udp", "-m", "udp", "--dport", strconv.Itoa(port.IntraClusterVxLAN), "-j", "ACCEPT"}

	if err = ipt.AppendUnique(constants.FilterTable, constants.SmInputChain, ruleSpec...); err != nil {
		return errors.Wrapf(err, "unable to append iptables rule %q", strings.Join(ruleSpec, " "))
	}

	logger.V(log.DEBUG).Infof("Insert rule to allow traffic over %s interface in FORWARDing Chain", VxLANIface)

	ruleSpec = []string{"-o", VxLANIface, "-j", "ACCEPT"}

	if err = ipt.PrependUnique(constants.FilterTable, "FORWARD", ruleSpec); err != nil {
		return errors.Wrap(err, "unable to insert iptable rule in filter table to allow vxlan traffic")
	}

	if kp.cniIface != nil {
		// Program rules to support communication from HostNetwork to remoteCluster
		sourceAddress := strconv.Itoa(VxLANVTepNetworkPrefix) + ".0.0.0/8"
		ruleSpec = []string{"-s", sourceAddress, "-o", VxLANIface, "-j", "SNAT", "--to", kp.cniIface.IPAddress}
		logger.V(log.DEBUG).Infof("Installing rule for host network to remote cluster communication: %s", strings.Join(ruleSpec, " "))

		if err = ipt.AppendUnique(constants.NATTable, constants.SmPostRoutingChain, ruleSpec...); err != nil {
			return errors.Wrapf(err, "error appending iptables rule %q", strings.Join(ruleSpec, " "))
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
	ipt, err := iptables.New()
	if err != nil {
		return errors.Wrap(err, "error initializing iptables")
	}

	for _, localClusterCidr := range kp.localClusterCidr {
		outboundRuleSpec := []string{"-s", localClusterCidr, "-d", remoteCidrBlock, "-j", "ACCEPT"}
		incomingRuleSpec := []string{"-s", remoteCidrBlock, "-d", localClusterCidr, "-j", "ACCEPT"}

		if operation == Add {
			logger.V(log.DEBUG).Infof("Installing iptables rule for outgoing traffic: %s", strings.Join(outboundRuleSpec, " "))

			if err = ipt.AppendUnique(constants.NATTable, constants.SmPostRoutingChain, outboundRuleSpec...); err != nil {
				return errors.Wrapf(err, "error appending iptables rule %q", strings.Join(outboundRuleSpec, " "))
			}

			logger.V(log.DEBUG).Infof("Installing iptables rule for incoming traffic: %s", strings.Join(incomingRuleSpec, " "))

			if err = ipt.AppendUnique(constants.NATTable, constants.SmPostRoutingChain, incomingRuleSpec...); err != nil {
				return errors.Wrapf(err, "error appending iptables rule %q", strings.Join(incomingRuleSpec, " "))
			}
		} else if operation == Delete {
			logger.V(log.DEBUG).Infof("Deleting iptables rule for outgoing traffic: %s", strings.Join(outboundRuleSpec, " "))

			if err = ipt.Delete(constants.NATTable, constants.SmPostRoutingChain, outboundRuleSpec...); err != nil {
				return errors.Wrapf(err, "error deleting iptables rule %q", strings.Join(outboundRuleSpec, " "))
			}

			logger.V(log.DEBUG).Infof("Deleting iptables rule for incoming traffic: %s", strings.Join(incomingRuleSpec, " "))

			if err = ipt.Delete(constants.NATTable, constants.SmPostRoutingChain, incomingRuleSpec...); err != nil {
				return errors.Wrapf(err, "error deleting iptables rule %q", strings.Join(incomingRuleSpec, " "))
			}
		}
	}

	return nil
}
