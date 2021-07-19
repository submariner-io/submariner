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
	"fmt"
	"strings"

	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/iptables"
)

const (
	postRoutingChain           = "POSTROUTING"
	subMarinerPostRoutingChain = "SUBMARINER-POSTROUTING"
	mangleTable                = "mangle"
)

type mtuHandler struct {
	event.HandlerBase
	ipt iptables.Interface
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
		return fmt.Errorf("error initializing iptables: %v", err)
	}

	if err := iptables.CreateChainIfNotExists(h.ipt, mangleTable, subMarinerPostRoutingChain); err != nil {
		return fmt.Errorf("error creating iptables chain %s: %v", subMarinerPostRoutingChain, err)
	}

	forwardToSubMarinerPostRoutingChain := []string{"-j", subMarinerPostRoutingChain}

	if err := iptables.PrependUnique(h.ipt, mangleTable, postRoutingChain, forwardToSubMarinerPostRoutingChain); err != nil {
		return fmt.Errorf("error inserting iptables rule %q: %v",
			strings.Join(forwardToSubMarinerPostRoutingChain, " "), err)
	}

	return nil
}

func (h *mtuHandler) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	subnets := extractSubnets(endpoint.Spec)
	for _, subnet := range subnets {
		err := h.addClampMssToPathMTU(subnet)

		if err != nil {
			return err
		}
	}

	return nil
}

func (h *mtuHandler) RemoteEndpointRemoved(endpoint *submV1.Endpoint) error {
	subnets := extractSubnets(endpoint.Spec)
	for _, subnet := range subnets {
		err := h.removeClampMssToPathMTU(subnet)

		if err != nil {
			return err
		}
	}

	return nil
}

func (h *mtuHandler) addClampMssToPathMTU(subnets string) error {
	ruleSpecSource := []string{"-s", subnets, "-p", "tcp", "-m", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j",
		"TCPMSS", "--clamp-mss-to-pmtu"}
	ruleSpecDest := []string{"-d", subnets, "-p", "tcp", "-m", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j",
		"TCPMSS", "--clamp-mss-to-pmtu"}

	if err := h.ipt.AppendUnique(mangleTable, subMarinerPostRoutingChain, ruleSpecSource...); err != nil {
		return fmt.Errorf("error appending iptables rule \"%s\": %v", strings.Join(ruleSpecSource, " "), err)
	}

	if err := h.ipt.AppendUnique(mangleTable, subMarinerPostRoutingChain, ruleSpecDest...); err != nil {
		return fmt.Errorf("error appending iptables rule \"%s\": %v", strings.Join(ruleSpecDest, " "), err)
	}

	return nil
}

func (h *mtuHandler) removeClampMssToPathMTU(subnets string) error {
	ruleSpecSource := []string{"-s", subnets, "-p", "tcp", "-m", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j",
		"TCPMSS", "--clamp-mss-to-pmtu"}
	ruleSpecDest := []string{"-d", subnets, "-p", "tcp", "-m", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j",
		"TCPMSS", "--clamp-mss-to-pmtu"}

	if err := h.ipt.Delete(mangleTable, subMarinerPostRoutingChain, ruleSpecSource...); err != nil {
		return fmt.Errorf("error deleting iptables rule \"%s\": %v", strings.Join(ruleSpecSource, " "), err)
	}

	if err := h.ipt.Delete(mangleTable, subMarinerPostRoutingChain, ruleSpecDest...); err != nil {
		return fmt.Errorf("error deleting iptables rule \"%s\": %v", strings.Join(ruleSpecDest, " "), err)
	}

	return nil
}

func extractSubnets(endpoint submV1.EndpointSpec) []string {
	subnets := make([]string, 0, len(endpoint.Subnets))

	for _, subnet := range endpoint.Subnets {
		if !strings.HasPrefix(subnet, endpoint.PrivateIP+"/") {
			subnets = append(subnets, subnet)
		}
	}

	return subnets
}
