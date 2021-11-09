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
	"strings"

	"github.com/pkg/errors"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/ipset"
	"github.com/submariner-io/submariner/pkg/iptables"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"k8s.io/klog"
	utilexec "k8s.io/utils/exec"
)

type mtuHandler struct {
	event.HandlerBase
	ipt         iptables.Interface
	remoteIPSet ipset.Named
	localIPSet  ipset.Named
}

func NewMTUHandler() event.Handler {
	return &mtuHandler{}
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

	if err := iptables.PrependUnique(h.ipt, constants.MangleTable, constants.PostRoutingChain,
		forwardToSubMarinerPostRoutingChain); err != nil {
		return errors.Wrapf(err, "error inserting iptables rule %q",
			strings.Join(forwardToSubMarinerPostRoutingChain, " "))
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
