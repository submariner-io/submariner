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
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cidr"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	k8snet "k8s.io/utils/net"
)

const (
	RemoteCIDRIPSetIPv4 = "SUBMARINER-REMOTECIDRS"
	LocalCIDRIPSetIPv4  = "SUBMARINER-LOCALCIDRS"
	RemoteCIDRIPSetIPv6 = "SUBMARINER-REMOTECIDRS-V6"
	LocalCIDRIPSetIPv6  = "SUBMARINER-LOCALCIDRS-V6"
)

type AvoidSNATHandler struct {
	event.HandlerBase
	ipFamily        k8snet.IPFamily
	pFilter         packetfilter.Interface
	remoteIPSet     packetfilter.NamedSet
	localIPSet      packetfilter.NamedSet
	localCIDRIPSet  string
	remoteCIDRIPSet string
}

type noopAvoidSNATHandler struct {
	event.HandlerBase
}

func (h *noopAvoidSNATHandler) GetName() string {
	return "submariner-noop-avoid-snat-handler"
}

func (h *noopAvoidSNATHandler) GetNetworkPlugins() []string {
	return []string{cni.OVNKubernetes}
}

func NewAvoidSNATHandler(ipFamily k8snet.IPFamily) event.Handler {
	// if no secondary driver is defined (primary driver is nftables) or its IPv6 return noop handler
	_, err := packetfilter.NewSecondary(ipFamily)
	if err != nil || ipFamily != k8snet.IPv4 {
		return &noopAvoidSNATHandler{}
	}

	h := &AvoidSNATHandler{
		ipFamily:        ipFamily,
		localCIDRIPSet:  LocalCIDRIPSetIPv4,
		remoteCIDRIPSet: RemoteCIDRIPSetIPv4,
	}

	if ipFamily == k8snet.IPv6 {
		h.localCIDRIPSet = LocalCIDRIPSetIPv6
		h.remoteCIDRIPSet = RemoteCIDRIPSetIPv6
	}

	return h
}

func (h *AvoidSNATHandler) Init(_ context.Context) error {
	logger.Info("Starting AvoidSNATHandler")

	var err error

	h.pFilter, err = packetfilter.NewSecondary(h.ipFamily)
	if err != nil {
		return errors.Wrap(err, "error initializing packetfilter")
	}

	if err := h.pFilter.CreateIPHookChainIfNotExists(&packetfilter.ChainIPHook{
		Name:     constants.SmSelfSnatChain,
		Type:     packetfilter.ChainTypeNAT,
		Hook:     packetfilter.ChainHookPostrouting,
		Priority: packetfilter.ChainPriorityMiddle,
	}); err != nil {
		return errors.Wrapf(err, "error reating IPHookChain chain %s", constants.SmSelfSnatChain)
	}

	// when nftables is set as primary driver this call should move to LocalEndpointCreated.
	return h.createSelfSNATRules()
}

func (h *AvoidSNATHandler) GetName() string {
	return "submariner-avoid-snat-handler"
}

func (h *AvoidSNATHandler) GetNetworkPlugins() []string {
	return []string{cni.OVNKubernetes}
}

func (h *AvoidSNATHandler) newNamedSetSet(key string) packetfilter.NamedSet {
	return h.pFilter.NewNamedSet(&packetfilter.SetInfo{
		Name: key,
	})
}

func (h *AvoidSNATHandler) createSelfSNATRules() error {
	// when nftables is set as primary driver , we can reuse sets created in MTUhandler.
	h.remoteIPSet = h.newNamedSetSet(h.remoteCIDRIPSet)
	if err := h.remoteIPSet.Create(true); err != nil {
		return errors.Wrapf(err, "error creating ipset %q", h.remoteCIDRIPSet)
	}

	h.localIPSet = h.newNamedSetSet(h.localCIDRIPSet)
	if err := h.localIPSet.Create(true); err != nil {
		return errors.Wrapf(err, "error creating ipset %q", h.localCIDRIPSet)
	}

	logger.Info("Creating packetfilter self-snat rules")

	ruleSpecIngress := packetfilter.Rule{
		SrcSetName:  h.remoteCIDRIPSet,
		DestSetName: h.localCIDRIPSet,
		Action:      packetfilter.RuleActionSelfSNAT,
	}

	if err := h.pFilter.AppendUnique(packetfilter.TableTypeNAT, constants.SmSelfSnatChain, &ruleSpecIngress); err != nil {
		return errors.Wrapf(err, "unable to append rule %+v", &ruleSpecIngress)
	}

	return nil
}

// when nftables is set as primary driver , we can remove these functions.
func (h *AvoidSNATHandler) LocalEndpointCreated(endpoint *submV1.Endpoint) error {
	subnets := cidr.ExtractSubnets(h.ipFamily, endpoint.Spec.Subnets)
	for _, subnet := range subnets {
		err := h.localIPSet.AddEntry(subnet, true)
		if err != nil {
			return errors.Wrap(err, "error adding local IP set entry")
		}
	}

	return nil
}

func (h *AvoidSNATHandler) LocalEndpointRemoved(endpoint *submV1.Endpoint) error {
	subnets := cidr.ExtractSubnets(h.ipFamily, endpoint.Spec.Subnets)
	for _, subnet := range subnets {
		logError(h.localIPSet.DelEntry(subnet), "Error deleting the subnet %q from the local IPSet", subnet)
	}

	return nil
}

func (h *AvoidSNATHandler) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	subnets := cidr.ExtractSubnets(h.ipFamily, endpoint.Spec.Subnets)
	for _, subnet := range subnets {
		err := h.remoteIPSet.AddEntry(subnet, true)
		if err != nil {
			return errors.Wrap(err, "error adding remote IP set entry")
		}
	}

	return nil
}

func (h *AvoidSNATHandler) RemoteEndpointRemoved(endpoint *submV1.Endpoint) error {
	subnets := cidr.ExtractSubnets(h.ipFamily, endpoint.Spec.Subnets)
	for _, subnet := range subnets {
		logError(h.remoteIPSet.DelEntry(subnet), "Error deleting the subnet %q from the remote IPSet", subnet)
	}

	return nil
}

func (h *AvoidSNATHandler) Uninstall() error {
	logger.Infof("Flushing packetfilter entry in %q chain of %q table", constants.SmSelfSnatChain, constants.NATTable)

	if err := h.pFilter.ClearChain(packetfilter.TableTypeNAT, constants.SmSelfSnatChain); err != nil {
		logger.Errorf(err, "Error flushing packetfilter chain %q of %q table", constants.SmSelfSnatChain,
			constants.NATTable)
	}

	logger.Infof("Deleting packetfilter entry in %q chain of %q table", constants.SmSelfSnatChain, constants.NATTable)

	logError(h.pFilter.DeleteIPHookChain(&packetfilter.ChainIPHook{
		Name:     constants.SmSelfSnatChain,
		Type:     packetfilter.ChainTypeNAT,
		Hook:     packetfilter.ChainHookPostrouting,
		Priority: packetfilter.ChainPriorityMiddle,
	}), "Error deleting IP hook chain %q of table type %q", constants.SmSelfSnatChain, packetfilter.ChainTypeNAT)

	logger.Infof("Flushing packetfilter entries in %q chain of %q table", constants.SmSelfSnatChain, constants.NATTable)

	logError(h.localIPSet.Flush(), "Error flushing ipset %q", h.localCIDRIPSet)

	logError(h.localIPSet.Destroy(), "Error deleting ipset %q", h.localCIDRIPSet)

	logError(h.remoteIPSet.Flush(), "Error flushing ipset %q", h.remoteCIDRIPSet)

	logError(h.remoteIPSet.Destroy(), "Error deleting ipset %q", h.remoteCIDRIPSet)

	return nil
}

func logError(err error, format string, args ...interface{}) {
	if err != nil {
		logger.Errorf(err, format, args...)
	}
}
