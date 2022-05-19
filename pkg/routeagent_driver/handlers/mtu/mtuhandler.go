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
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable/vxlan"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/ipset"
	"github.com/submariner-io/submariner/pkg/iptables"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"k8s.io/klog"
	utilexec "k8s.io/utils/exec"
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
}

func NewMTUHandler(localClusterCidr []string, isGlobalnet bool) event.Handler {
	forceMss := notNeeded
	if isGlobalnet {
		forceMss = needed
	}

	return &mtuHandler{
		localClusterCidr: localClusterCidr,
		forceMss:         forceMss,
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

	if err := iptables.CreateChainIfNotExists(h.ipt, constants.MangleTable, constants.SmPostRoutingChain); err != nil {
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

	if err := iptables.PrependUnique(h.ipt, constants.MangleTable, constants.PostRoutingChain,
		forwardToSubMarinerPostRoutingChain); err != nil {
		return errors.Wrapf(err, "error inserting iptables rule %q",
			strings.Join(forwardToSubMarinerPostRoutingChain, " "))
	}

	// iptable rules to clamp TCP MSS to a fixed value will be programmed when the local endpoint is created
	if h.forceMss == needed {
		return nil
	}

	klog.Info("Creating iptables clamp-mss-to-pmtu rules")

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
	subnets := extractSubnets(&endpoint.Spec)
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
		klog.Info("Creating iptables set-mss rules")

		err := h.forceMssClamping(endpoint)
		if err != nil {
			return errors.Wrap(err, "error forcing TCP MSS clamping")
		}

		h.forceMss = configured
	}

	return nil
}

func (h *mtuHandler) LocalEndpointRemoved(endpoint *submV1.Endpoint) error {
	subnets := extractSubnets(&endpoint.Spec)
	for _, subnet := range subnets {
		err := h.localIPSet.DelEntry(subnet)
		if err != nil {
			klog.Errorf("Error deleting the subnet %q from the local IPSet: %v", subnet, err)
		}
	}

	for _, subnet := range h.localClusterCidr {
		err := h.localIPSet.DelEntry(subnet)
		if err != nil {
			klog.Errorf("Error deleting the subnet %q from the local IPSet: %v", subnet, err)
		}
	}

	return nil
}

func (h *mtuHandler) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	subnets := extractSubnets(&endpoint.Spec)
	for _, subnet := range subnets {
		err := h.remoteIPSet.AddEntry(subnet, true)
		if err != nil {
			return errors.Wrap(err, "error adding remote IP set entry")
		}
	}

	return nil
}

func (h *mtuHandler) RemoteEndpointRemoved(endpoint *submV1.Endpoint) error {
	subnets := extractSubnets(&endpoint.Spec)
	for _, subnet := range subnets {
		err := h.remoteIPSet.DelEntry(subnet)
		if err != nil {
			klog.Errorf("Error deleting the subnet %q from the remote IPSet: %v", subnet, err)
		}
	}

	return nil
}

func extractSubnets(endpoint *submV1.EndpointSpec) []string {
	subnets := make([]string, 0, len(endpoint.Subnets))

	for _, subnet := range endpoint.Subnets {
		if !strings.HasPrefix(subnet, endpoint.PrivateIP+"/") {
			subnets = append(subnets, subnet)
		}
	}

	return subnets
}

func (h *mtuHandler) newNamedIPSet(key string, ipSetIface ipset.Interface) ipset.Named {
	return ipset.NewNamed(&ipset.IPSet{
		Name:    key,
		SetType: ipset.HashNet,
	}, ipSetIface)
}

func (h *mtuHandler) Stop(uninstall bool) error {
	if !uninstall {
		return nil
	}

	klog.Infof("Flushing iptable entries in %q chain of %q table", constants.SmPostRoutingChain, constants.MangleTable)

	if err := h.ipt.ClearChain(constants.MangleTable, constants.SmPostRoutingChain); err != nil {
		klog.Errorf("Error flushing iptables chain %q of %q table: %v", constants.SmPostRoutingChain,
			constants.MangleTable, err)
	}

	klog.Infof("Deleting iptable entry in %q chain of %q table", constants.PostRoutingChain, constants.MangleTable)

	ruleSpec := []string{"-j", constants.SmPostRoutingChain}
	if err := h.ipt.Delete(constants.MangleTable, constants.PostRoutingChain, ruleSpec...); err != nil {
		klog.Errorf("Error deleting iptables rule from %q chain: %v", constants.PostRoutingChain, err)
	}

	klog.Infof("Deleting iptable %q chain of %q table", constants.SmPostRoutingChain, constants.MangleTable)

	if err := h.ipt.DeleteChain(constants.MangleTable, constants.SmPostRoutingChain); err != nil {
		klog.Errorf("Error deleting iptable chain %q of table %q: %v", constants.SmPostRoutingChain,
			constants.MangleTable, err)
	}

	if err := h.localIPSet.Flush(); err != nil {
		klog.Errorf("Error flushing ipset %q: %v", constants.LocalCIDRIPSet, err)
	}

	if err := h.localIPSet.Destroy(); err != nil {
		klog.Errorf("Error deleting ipset %q: %v", constants.LocalCIDRIPSet, err)
	}

	if err := h.remoteIPSet.Flush(); err != nil {
		klog.Errorf("Error flushing ipset %q: %v", constants.RemoteCIDRIPSet, err)
	}

	if err := h.remoteIPSet.Destroy(); err != nil {
		klog.Errorf("Error deleting ipset %q: %v", constants.RemoteCIDRIPSet, err)
	}

	return nil
}

func (h *mtuHandler) forceMssClamping(endpoint *submV1.Endpoint) error {
	defaultHostIface, err := netlinkAPI.GetDefaultGatewayInterface()
	if err != nil {
		return errors.Wrapf(err, "Unable to find the default interface on host")
	}

	overHeadSize := maxIpsecOverhead
	if endpoint.Spec.Backend == vxlan.CableDriverName {
		overHeadSize = vxlan.VxlanOverhead
	}

	klog.Infof("forceMssClamping to: IF_MTU(%d)-Overhead(%d)=%d",
		defaultHostIface.MTU, overHeadSize, (defaultHostIface.MTU - overHeadSize))

	ruleSpecSource := []string{
		"-m", "set", "--match-set", constants.LocalCIDRIPSet, "src", "-m", "set", "--match-set",
		constants.RemoteCIDRIPSet, "dst", "-p", "tcp", "-m", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS",
		"--set-mss", strconv.Itoa(defaultHostIface.MTU - overHeadSize),
	}
	ruleSpecDest := []string{
		"-m", "set", "--match-set", constants.RemoteCIDRIPSet, "src", "-m", "set", "--match-set",
		constants.LocalCIDRIPSet, "dst", "-p", "tcp", "-m", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS",
		"--set-mss", strconv.Itoa(defaultHostIface.MTU - overHeadSize),
	}

	if err := h.ipt.AppendUnique(constants.MangleTable, constants.SmPostRoutingChain, ruleSpecSource...); err != nil {
		return errors.Wrapf(err, "error appending iptables rule %q", strings.Join(ruleSpecSource, " "))
	}

	if err := h.ipt.AppendUnique(constants.MangleTable, constants.SmPostRoutingChain, ruleSpecDest...); err != nil {
		return errors.Wrapf(err, "error appending iptables rule %q", strings.Join(ruleSpecDest, " "))
	}

	return nil
}
