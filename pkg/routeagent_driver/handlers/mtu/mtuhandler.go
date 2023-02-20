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

package mtu

import (
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable/vxlan"
	"github.com/submariner-io/submariner/pkg/cidr"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/ipset"
	"github.com/submariner-io/submariner/pkg/iptables"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	utilexec "k8s.io/utils/exec"
	k8snet "k8s.io/utils/net"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type forceMssSts int

const (
	notNeeded forceMssSts = iota
	needed
	configured
)

const (
	// TCP MSS = Default_Iface_MTU - TCP_H(20)-IP_H(20)-max_IpsecOverhed(80).
	maxIpsecOverhead = 120
)

type mtuHandler struct {
	event.HandlerBase
	localClusterCidr []string
	ipt              iptables.Interface
	remoteIPSet      ipset.Named
	localIPSet       ipset.Named
	forceMss         forceMssSts
	tcpMssValue      int
}

var logger = log.Logger{Logger: logf.Log.WithName("MTU")}

func NewMTUHandler(localClusterCidr []string, isGlobalnet bool, tcpMssValue int) event.Handler {
	forceMss := notNeeded
	if isGlobalnet || tcpMssValue != 0 {
		forceMss = needed
	}

	return &mtuHandler{
		localClusterCidr: cidr.GetIPv4Subnets(localClusterCidr),
		forceMss:         forceMss,
		tcpMssValue:      tcpMssValue,
	}
}

func (h *mtuHandler) GetNetworkPlugins() []string {
	return []string{event.AnyNetworkPlugin}
}

func (h *mtuHandler) GetName() string {
	return "MTU handler"
}

func (h *mtuHandler) Init() error {
	var err error

	h.ipt, err = iptables.New()
	if err != nil {
		return errors.Wrap(err, "error initializing iptables")
	}

	ipSetIface := ipset.New(utilexec.New())

	if err := h.ipt.CreateChainIfNotExists(constants.MangleTable, constants.SmPostRoutingChain); err != nil {
		return errors.Wrapf(err, "error creating iptables chain %s", constants.SmPostRoutingChain)
	}

	forwardToSubMarinerPostRoutingChain := []string{"-j", constants.SmPostRoutingChain}

	h.remoteIPSet = h.newNamedIPSet(constants.RemoteCIDRIPSet, ipSetIface)
	if err := h.remoteIPSet.Create(true); err != nil {
		return errors.Wrapf(err, "error creating ipset %q", constants.RemoteCIDRIPSet)
	}

	h.localIPSet = h.newNamedIPSet(constants.LocalCIDRIPSet, ipSetIface)
	if err := h.localIPSet.Create(true); err != nil {
		return errors.Wrapf(err, "error creating ipset %q", constants.LocalCIDRIPSet)
	}

	if err := h.ipt.PrependUnique(constants.MangleTable, constants.PostRoutingChain,
		forwardToSubMarinerPostRoutingChain); err != nil {
		return errors.Wrapf(err, "error inserting iptables rule %q",
			strings.Join(forwardToSubMarinerPostRoutingChain, " "))
	}

	// iptable rules to clamp TCP MSS to a fixed value will be programmed when the local endpoint is created
	if h.forceMss == needed {
		return nil
	}

	logger.Info("Creating iptables clamp-mss-to-pmtu rules")

	ruleSpecSource := []string{
		"-m", "set", "--match-set", constants.LocalCIDRIPSet, "src", "-m", "set", "--match-set",
		constants.RemoteCIDRIPSet, "dst", "-p", "tcp", "-m", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS",
		"--clamp-mss-to-pmtu",
	}
	ruleSpecDest := []string{
		"-m", "set", "--match-set", constants.RemoteCIDRIPSet, "src", "-m", "set", "--match-set",
		constants.LocalCIDRIPSet, "dst", "-p", "tcp", "-m", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS",
		"--clamp-mss-to-pmtu",
	}

	if err := h.ipt.AppendUnique(constants.MangleTable, constants.SmPostRoutingChain, ruleSpecSource...); err != nil {
		return errors.Wrapf(err, "error appending iptables rule %q", strings.Join(ruleSpecSource, " "))
	}

	if err := h.ipt.AppendUnique(constants.MangleTable, constants.SmPostRoutingChain, ruleSpecDest...); err != nil {
		return errors.Wrapf(err, "error appending iptables rule %q", strings.Join(ruleSpecSource, " "))
	}

	return nil
}

func (h *mtuHandler) LocalEndpointCreated(endpoint *submV1.Endpoint) error {
	subnets := extractIPv4Subnets(&endpoint.Spec)
	for _, subnet := range subnets {
		err := h.localIPSet.AddEntry(subnet, true)
		if err != nil {
			return errors.Wrap(err, "error adding local IP set entry")
		}
	}

	for _, subnet := range h.localClusterCidr {
		err := h.localIPSet.AddEntry(subnet, true)
		if err != nil {
			return errors.Wrap(err, "error adding localClusterCidr IP set entry")
		}
	}

	if h.forceMss == needed {
		logger.Info("Creating iptables set-mss rules")

		err := h.forceMssClamping(endpoint)
		if err != nil {
			return errors.Wrap(err, "error forcing TCP MSS clamping")
		}

		h.forceMss = configured
	}

	return nil
}

func (h *mtuHandler) LocalEndpointRemoved(endpoint *submV1.Endpoint) error {
	subnets := extractIPv4Subnets(&endpoint.Spec)
	for _, subnet := range subnets {
		err := h.localIPSet.DelEntry(subnet)
		if err != nil {
			logger.Errorf(err, "Error deleting the subnet %q from the local IPSet", subnet)
		}
	}

	for _, subnet := range h.localClusterCidr {
		err := h.localIPSet.DelEntry(subnet)
		if err != nil {
			logger.Errorf(err, "Error deleting the subnet %q from the local IPSet", subnet)
		}
	}

	return nil
}

func (h *mtuHandler) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	subnets := extractIPv4Subnets(&endpoint.Spec)
	for _, subnet := range subnets {
		err := h.remoteIPSet.AddEntry(subnet, true)
		if err != nil {
			return errors.Wrap(err, "error adding remote IP set entry")
		}
	}

	return nil
}

func (h *mtuHandler) RemoteEndpointRemoved(endpoint *submV1.Endpoint) error {
	subnets := extractIPv4Subnets(&endpoint.Spec)
	for _, subnet := range subnets {
		err := h.remoteIPSet.DelEntry(subnet)
		if err != nil {
			logger.Errorf(err, "Error deleting the subnet %q from the remote IPSet", subnet)
		}
	}

	return nil
}

func extractIPv4Subnets(endpoint *submV1.EndpointSpec) []string {
	subnets := make([]string, 0, len(endpoint.Subnets))

	for _, subnet := range endpoint.Subnets {
		// Revisit when IPv6 support is added.
		if k8snet.IsIPv4CIDRString(subnet) {
			subnets = append(subnets, subnet)
		}
	}

	return subnets
}

func (h *mtuHandler) newNamedIPSet(key string, ipSetIface ipset.Interface) ipset.Named {
	return ipset.NewNamed(&ipset.IPSet{
		Name:       key,
		SetType:    ipset.HashNet,
		HashFamily: ipset.ProtocolFamilyIPV4,
	}, ipSetIface)
}

func (h *mtuHandler) Stop(uninstall bool) error {
	if !uninstall {
		return nil
	}

	logger.Infof("Flushing iptable entries in %q chain of %q table", constants.SmPostRoutingChain, constants.MangleTable)

	if err := h.ipt.ClearChain(constants.MangleTable, constants.SmPostRoutingChain); err != nil {
		logger.Errorf(err, "Error flushing iptables chain %q of %q table", constants.SmPostRoutingChain,
			constants.MangleTable)
	}

	logger.Infof("Deleting iptable entry in %q chain of %q table", constants.PostRoutingChain, constants.MangleTable)

	ruleSpec := []string{"-j", constants.SmPostRoutingChain}
	if err := h.ipt.Delete(constants.MangleTable, constants.PostRoutingChain, ruleSpec...); err != nil {
		logger.Errorf(err, "Error deleting iptables rule from %q chain", constants.PostRoutingChain)
	}

	logger.Infof("Deleting iptable %q chain of %q table", constants.SmPostRoutingChain, constants.MangleTable)

	if err := h.ipt.DeleteChain(constants.MangleTable, constants.SmPostRoutingChain); err != nil {
		logger.Errorf(err, "Error deleting iptable chain %q of table %q", constants.SmPostRoutingChain,
			constants.MangleTable)
	}

	if err := h.localIPSet.Flush(); err != nil {
		logger.Errorf(err, "Error flushing ipset %q", constants.LocalCIDRIPSet)
	}

	if err := h.localIPSet.Destroy(); err != nil {
		logger.Errorf(err, "Error deleting ipset %q", constants.LocalCIDRIPSet)
	}

	if err := h.remoteIPSet.Flush(); err != nil {
		logger.Errorf(err, "Error flushing ipset %q", constants.RemoteCIDRIPSet)
	}

	if err := h.remoteIPSet.Destroy(); err != nil {
		logger.Errorf(err, "Error deleting ipset %q", constants.RemoteCIDRIPSet)
	}

	return nil
}

func (h *mtuHandler) forceMssClamping(endpoint *submV1.Endpoint) error {
	tcpMssSrc := "user"
	tcpMssValue := h.tcpMssValue

	if tcpMssValue == 0 {
		defaultHostIface, err := netlinkAPI.GetDefaultGatewayInterface()
		if err != nil {
			return errors.Wrapf(err, "Unable to find the default interface on host")
		}

		overHeadSize := maxIpsecOverhead
		if endpoint.Spec.Backend == vxlan.CableDriverName {
			overHeadSize = vxlan.VxlanOverhead
		}

		tcpMssValue = defaultHostIface.MTU - overHeadSize
		tcpMssSrc = "default"
	}

	logger.Infof("forceMssClamping to: %d (%s) ", tcpMssValue, tcpMssSrc)
	ruleSpecSource := []string{
		"-m", "set", "--match-set", constants.LocalCIDRIPSet, "src", "-m", "set", "--match-set",
		constants.RemoteCIDRIPSet, "dst", "-p", "tcp", "-m", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS",
		"--set-mss", strconv.Itoa(tcpMssValue),
	}
	ruleSpecDest := []string{
		"-m", "set", "--match-set", constants.RemoteCIDRIPSet, "src", "-m", "set", "--match-set",
		constants.LocalCIDRIPSet, "dst", "-p", "tcp", "-m", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS",
		"--set-mss", strconv.Itoa(tcpMssValue),
	}

	rules, err := h.ipt.List(constants.MangleTable, constants.SmPostRoutingChain)
	if err != nil {
		return errors.Wrapf(err, "error listing the rules in %s chain", constants.SmPostRoutingChain)
	}

	isPresent := false

	for _, rule := range rules {
		if strings.Contains(rule, strings.Join(ruleSpecSource, " ")) || strings.Contains(rule, strings.Join(ruleSpecDest, " ")) {
			isPresent = true
			break
		}
	}

	if len(rules) > 0 && !isPresent {
		if err := h.ipt.ClearChain(constants.MangleTable, constants.SmPostRoutingChain); err != nil {
			logger.Warningf("Error flushing iptables chain %q of %q table: %v", constants.SmPostRoutingChain,
				constants.MangleTable, err)
		}
	}

	if err := h.ipt.AppendUnique(constants.MangleTable, constants.SmPostRoutingChain, ruleSpecSource...); err != nil {
		return errors.Wrapf(err, "error appending iptables rule %q", strings.Join(ruleSpecSource, " "))
	}

	if err := h.ipt.AppendUnique(constants.MangleTable, constants.SmPostRoutingChain, ruleSpecDest...); err != nil {
		return errors.Wrapf(err, "error appending iptables rule %q", strings.Join(ruleSpecDest, " "))
	}

	return nil
}
