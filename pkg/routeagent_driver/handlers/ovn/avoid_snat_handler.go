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
	"strconv"

	"github.com/pkg/errors"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cidr"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	"github.com/submariner-io/submariner/pkg/packetfilter/nftables"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	corev1 "k8s.io/api/core/v1"
)

const (
	enableSNATHandlerKey = "enable-snat-handler"
	remoteCIDRIPSetIPv4  = "SUBMARINER-REMOTECIDRS"
	localCIDRIPSetIPv4   = "SUBMARINER-LOCALCIDRS"
)

type AvoidSNATHandler struct {
	event.HandlerBase
	pFilter     packetfilter.Interface
	remoteIPSet packetfilter.NamedSet
	localIPSet  packetfilter.NamedSet
}

func NeedToEnableAvoidSNATHandler(cm *corev1.ConfigMap) (bool, error) {
	enableHandler := false

	if cm != nil {
		if value, ok := cm.Data[enableSNATHandlerKey]; ok {
			var err error

			enableHandler, err = strconv.ParseBool(value)
			if err != nil {
				return false, errors.Wrapf(err, "unable to parse %q from ConfigMap %q", enableSNATHandlerKey, cm.Name)
			}
		}
	}

	return enableHandler, nil
}

func NewAvoidSNATHandler() event.Handler {

	h := &AvoidSNATHandler{}

	return h
}

func (h *AvoidSNATHandler) Init(_ context.Context) error {
	logger.Info("Starting AvoidSNATHandler")

	var err error

	h.pFilter, err = packetfilter.NewWithDriver(nftables.New)
	if err != nil {
		return errors.Wrap(err, "error initializing packetfilter with nftables driver")
	}

	if err := h.pFilter.CreateIPHookChainIfNotExists(&packetfilter.ChainIPHook{
		Name:     constants.SmSelfSnatChain,
		Type:     packetfilter.ChainTypeNAT,
		Hook:     packetfilter.ChainHookPostrouting,
		Priority: packetfilter.ChainPriorityMiddle,
	}); err != nil {
		return errors.Wrapf(err, "error creating IPHookChain chain %s", constants.SmSelfSnatChain)
	}

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
	h.remoteIPSet = h.newNamedSetSet(remoteCIDRIPSetIPv4)
	if err := h.remoteIPSet.Create(true); err != nil {
		return errors.Wrapf(err, "error creating set %q", remoteCIDRIPSetIPv4)
	}

	h.localIPSet = h.newNamedSetSet(localCIDRIPSetIPv4)
	if err := h.localIPSet.Create(true); err != nil {
		return errors.Wrapf(err, "error creating set %q", localCIDRIPSetIPv4)
	}

	logger.Info("Creating packetfilter self-snat rules")

	ruleSpecIngress := packetfilter.Rule{
		SrcSetName:  remoteCIDRIPSetIPv4,
		DestSetName: localCIDRIPSetIPv4,
		Action:      packetfilter.RuleActionSelfSNAT,
	}

	if err := h.pFilter.AppendUnique(packetfilter.TableTypeNAT, constants.SmSelfSnatChain, &ruleSpecIngress); err != nil {
		return errors.Wrapf(err, "unable to append rule %+v", &ruleSpecIngress)
	}

	return nil
}

func (h *AvoidSNATHandler) LocalEndpointCreated(endpoint *submV1.Endpoint) error {
	subnets := cidr.ExtractIPv4Subnets(endpoint.Spec.Subnets)
	for _, subnet := range subnets {
		err := h.localIPSet.AddEntry(subnet, true)
		if err != nil {
			return errors.Wrap(err, "error adding local IP set entry")
		}
	}

	return nil
}

func (h *AvoidSNATHandler) LocalEndpointRemoved(endpoint *submV1.Endpoint) error {
	subnets := cidr.ExtractIPv4Subnets(endpoint.Spec.Subnets)
	for _, subnet := range subnets {
		logError(h.localIPSet.DelEntry(subnet), "Error deleting the subnet %q from the local IPSet", subnet)
	}

	return nil
}

func (h *AvoidSNATHandler) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	subnets := cidr.ExtractIPv4Subnets(endpoint.Spec.Subnets)
	for _, subnet := range subnets {
		err := h.remoteIPSet.AddEntry(subnet, true)
		if err != nil {
			return errors.Wrap(err, "error adding remote IP set entry")
		}
	}

	return nil
}

func (h *AvoidSNATHandler) RemoteEndpointRemoved(endpoint *submV1.Endpoint) error {
	subnets := cidr.ExtractIPv4Subnets(endpoint.Spec.Subnets)
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

	logError(h.localIPSet.Flush(), "Error flushing ipset %q", localCIDRIPSetIPv4)

	logError(h.localIPSet.Destroy(), "Error deleting ipset %q", localCIDRIPSetIPv4)

	logError(h.remoteIPSet.Flush(), "Error flushing ipset %q", remoteCIDRIPSetIPv4)

	logError(h.remoteIPSet.Destroy(), "Error deleting ipset %q", remoteCIDRIPSetIPv4)

	return nil
}

func logError(err error, format string, args ...interface{}) {
	if err != nil {
		logger.Errorf(err, format, args...)
	}
}
