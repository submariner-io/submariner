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

package iptables

import (
	"strings"

	"github.com/coreos/go-iptables/iptables"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/ipset"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	utilexec "k8s.io/utils/exec"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	remoteCIDRIPSet                    = "SUBMARINER-REMOTECIDRS"
	localCIDRIPSet                     = "SUBMARINER-LOCALCIDRS"
	smPostRoutingChain                 = "SUBMARINER-POSTROUTING"
	smGlobalnetEgressChainForPods      = "SM-GN-EGRESS-PODS"
	smGlobalnetEgressChainForNamespace = "SM-GN-EGRESS-NS"
	gnIPSetPrefix                      = "SM-GN-"
	DriverName                         = "IPTables"
)

var (
	tableTypeToStr             = [packetfilter.TableTypeMAX]string{"filter", "mangle", "nat"}
	iphookChainTypeToStr       = [packetfilter.ChainTypeMAX]string{"filter", "mangle", "nat"}
	iphookChainTypeToTableType = [packetfilter.ChainTypeMAX]packetfilter.TableType{
		packetfilter.TableTypeFilter,
		packetfilter.TableTypeRoute,
		packetfilter.TableTypeNAT,
	}
	chainHookToStr  = [packetfilter.ChainHookMAX]string{"PREROUTING", "INPUT", "FORWARD", "OUTPUT", "POSTROUTING"}
	ruleActiontoStr = [packetfilter.RuleActionMAX]string{"", "ACCEPT", "TCPMSS", "MARK", "SNAT", "DNAT"}
	logger          = log.Logger{Logger: logf.Log.WithName(DriverName)}
)

type PacketFilter struct {
	ipt        *iptables.IPTables
	ipSetIface ipset.Interface
}

func New() (packetfilter.Driver, error) {
	ipt, err := iptables.New(iptables.IPFamily(iptables.ProtocolIPv4), iptables.Timeout(5))
	if err != nil {
		return nil, errors.Wrap(err, "error creating IP tables")
	}

	ipSetIface := ipset.New(utilexec.New())

	return &PacketFilter{
		ipt:        ipt,
		ipSetIface: ipSetIface,
	}, nil
}

func (a *PacketFilter) ChainExists(table packetfilter.TableType, chain string) (bool, error) {
	ok, err := a.ipt.ChainExists(tableTypeToStr[table], chain)
	return ok, errors.Wrap(err, "ChainExists failed")
}

func (a *PacketFilter) AppendUnique(table packetfilter.TableType, chain string, rule *packetfilter.Rule) error {
	ruleSpecStr, err := ToRuleSpec(rule)
	if err != nil {
		return errors.Wrap(err, "AppendUnique failed")
	}

	return errors.Wrap(a.ipt.AppendUnique(tableTypeToStr[table], chain, ruleSpecStr...), "error AppendUnique rule")
}

func (a *PacketFilter) CreateIPHookChainIfNotExists(chain *packetfilter.ChainIPHook) error {
	if err := a.createChainIfNotExists(iphookChainTypeToStr[chain.Type], chain.Name); err != nil {
		return errors.Wrapf(err, "error creating IP tables %s:%s chain", iphookChainTypeToStr[chain.Type], chain.Name)
	}

	jumpRule := chain.JumpRule
	if jumpRule == nil {
		jumpRule = &packetfilter.Rule{
			TargetChain: chain.Name,
			Action:      packetfilter.RuleActionJump,
		}
	}

	if chain.Priority == packetfilter.ChainPriorityFirst {
		ruleSpecStr, err := ToRuleSpec(jumpRule)
		if err != nil {
			return errors.Wrap(err, "Failed to translate packetfilter Rule to str")
		}

		table := tableTypeToStr[iphookChainTypeToTableType[chain.Type]]

		if err := a.ipt.InsertUnique(table, chainHookToStr[chain.Hook], 1, ruleSpecStr...); err != nil {
			return errors.Wrap(err, "error InsertUnique rule")
		}
	} else {
		if err := a.AppendUnique(iphookChainTypeToTableType[chain.Type], chainHookToStr[chain.Hook], jumpRule); err != nil {
			return errors.Wrap(err, "error AppendUnique rule")
		}
	}

	return nil
}

func (a *PacketFilter) CreateChainIfNotExists(table packetfilter.TableType, chain *packetfilter.Chain) error {
	if err := a.createChainIfNotExists(tableTypeToStr[table], chain.Name); err != nil {
		return errors.Wrapf(err, "error creating IP tables %s chain", chain.Name)
	}

	return nil
}

func (a *PacketFilter) DeleteIPHookChain(chain *packetfilter.ChainIPHook) error {
	tableType := iphookChainTypeToTableType[chain.Type]
	jumpRule := chain.JumpRule

	if jumpRule == nil {
		jumpRule = &packetfilter.Rule{
			TargetChain: chain.Name,
			Action:      packetfilter.RuleActionJump,
		}
	}

	if err := a.Delete(tableType, chainHookToStr[chain.Hook], jumpRule); err != nil {
		return errors.Wrap(err, "error deleting Jump Rule")
	}

	if err := a.ipt.DeleteChain(tableTypeToStr[tableType], chain.Name); err != nil {
		return errors.Wrap(err, "error deleting chain")
	}

	return nil
}

func (a *PacketFilter) DeleteChain(table packetfilter.TableType, chain string) error {
	if err := a.ipt.DeleteChain(tableTypeToStr[table], chain); err != nil {
		return errors.Wrap(err, "error deleting chain")
	}

	return nil
}

func (a *PacketFilter) ClearChain(table packetfilter.TableType, chain string) error {
	if err := a.ipt.ClearChain(tableTypeToStr[table], chain); err != nil {
		return errors.Wrap(err, "error clearing chain")
	}

	return nil
}

func (a *PacketFilter) Delete(table packetfilter.TableType, chain string, rule *packetfilter.Rule) error {
	ruleSpecStr, err := ToRuleSpec(rule)
	if err != nil {
		return errors.Wrap(err, "error translating ruleSpec to str")
	}

	return a.delete(tableTypeToStr[table], chain, ruleSpecStr...)
}

func (a *PacketFilter) List(table packetfilter.TableType, chain string) ([]*packetfilter.Rule, error) {
	existingRules, err := a.ipt.List(tableTypeToStr[table], chain)
	if err != nil {
		return []*packetfilter.Rule{}, errors.Wrapf(err, "error listing the rules in table %q, chain %q", tableTypeToStr[table], chain)
	}

	rules := []*packetfilter.Rule{}

	for _, existingRule := range existingRules {
		ruleSpec := strings.Split(existingRule, " ")
		if ruleSpec[0] == "-A" {
			ruleSpec = ruleSpec[2:] // remove "-A", "$chain"
		}

		rules = append(rules, FromRuleSpec(ruleSpec))
	}

	return rules, nil
}

func (a *PacketFilter) Insert(table packetfilter.TableType, chain string, pos int, rule *packetfilter.Rule) error {
	ruleSpecStr, err := ToRuleSpec(rule)
	if err != nil {
		return errors.Wrap(err, "Insert failed")
	}

	return errors.Wrap(a.ipt.Insert(tableTypeToStr[table], chain, pos, ruleSpecStr...), "error inserting ruleSpec to str")
}

func (a *PacketFilter) Append(table packetfilter.TableType, chain string, rule *packetfilter.Rule) error {
	ruleSpecStr, err := ToRuleSpec(rule)
	if err != nil {
		return errors.Wrap(err, "Append failed")
	}

	return errors.Wrap(a.ipt.Append(tableTypeToStr[table], chain, ruleSpecStr...), "error appending ruleSpec to str")
}

func (a *PacketFilter) createChainIfNotExists(table, chain string) error {
	exists, err := a.ipt.ChainExists(table, chain)
	if err == nil && exists {
		return nil
	}

	if err != nil {
		return errors.Wrapf(err, "error finding IP table chain %q in table %q", chain, table)
	}

	return errors.Wrap(a.ipt.NewChain(table, chain), "error creating IP table chain")
}

func (a *PacketFilter) delete(table, chain string, rulespec ...string) error {
	err := a.ipt.Delete(table, chain, rulespec...)

	var iptError *iptables.Error

	ok := errors.As(err, &iptError)
	if ok && iptError.IsNotExist() {
		return nil
	}

	return errors.Wrap(err, "error deleting IP table rule")
}

func protoToRuleSpec(ruleSpec *[]string, proto packetfilter.RuleProto) {
	switch proto {
	case packetfilter.RuleProtoUDP:
		*ruleSpec = append(*ruleSpec, "-p", "udp", "-m", "udp")
	case packetfilter.RuleProtoTCP:
		*ruleSpec = append(*ruleSpec, "-p", "tcp", "-m", "tcp")
	case packetfilter.RuleProtoICMP:
		*ruleSpec = append(*ruleSpec, "-p", "icmp")
	case packetfilter.RuleProtoAll:
		*ruleSpec = append(*ruleSpec, "-p", "all")
	case packetfilter.RuleProtoUndefined:
	}
}

func mssClampToRuleSpec(ruleSpec *[]string, clampType packetfilter.MssClampType, mssValue string) {
	switch clampType {
	case packetfilter.UndefinedMSS:
	case packetfilter.ToPMTU:
		*ruleSpec = append(*ruleSpec, "-p", "tcp", "-m", "tcp", "--tcp-flags", "SYN,RST", "SYN", "--clamp-mss-to-pmtu")
	case packetfilter.ToValue:
		*ruleSpec = append(*ruleSpec, "-p", "tcp", "-m", "tcp", "--tcp-flags", "SYN,RST", "SYN", "--set-mss", mssValue)
	}
}

func setToRuleSpec(ruleSpec *[]string, srcSetName, destSetName string) {
	if srcSetName != "" {
		*ruleSpec = append(*ruleSpec, "-m", "set", "--match-set", srcSetName, "src")
	}

	if destSetName != "" {
		*ruleSpec = append(*ruleSpec, "-m", "set", "--match-set", destSetName, "dst")
	}
}

func ToRuleSpec(rule *packetfilter.Rule) ([]string, error) {
	var ruleSpec []string
	protoToRuleSpec(&ruleSpec, rule.Proto)

	if rule.SrcCIDR != "" {
		ruleSpec = append(ruleSpec, "-s", rule.SrcCIDR)
	}

	if rule.DestCIDR != "" {
		ruleSpec = append(ruleSpec, "-d", rule.DestCIDR)
	}

	if rule.MarkValue != "" && rule.Action != packetfilter.RuleActionMark {
		ruleSpec = append(ruleSpec, "-m", "mark", "--mark", rule.MarkValue)
	}

	setToRuleSpec(&ruleSpec, rule.SrcSetName, rule.DestSetName)

	if rule.OutInterface != "" {
		ruleSpec = append(ruleSpec, "-o", rule.OutInterface)
	}

	if rule.InInterface != "" {
		ruleSpec = append(ruleSpec, "-i", rule.InInterface)
	}

	if rule.DPort != "" {
		ruleSpec = append(ruleSpec, "--dport", rule.DPort)
	}

	switch rule.Action {
	case packetfilter.RuleActionMAX:
	case packetfilter.RuleActionJump:
		ruleSpec = append(ruleSpec, "-j", rule.TargetChain)
	case packetfilter.RuleActionAccept, packetfilter.RuleActionMss,
		packetfilter.RuleActionMark, packetfilter.RuleActionSNAT, packetfilter.RuleActionDNAT:
		ruleSpec = append(ruleSpec, "-j", ruleActiontoStr[rule.Action])
	default:
		return ruleSpec, errors.Errorf(" rule.Action %d is invalid", rule.Action)
	}

	if rule.SnatCIDR != "" {
		ruleSpec = append(ruleSpec, "--to-source", rule.SnatCIDR)
	}

	if rule.DnatCIDR != "" {
		ruleSpec = append(ruleSpec, "--to-destination", rule.DnatCIDR)
	}

	mssClampToRuleSpec(&ruleSpec, rule.ClampType, rule.MssValue)

	if rule.MarkValue != "" && rule.Action == packetfilter.RuleActionMark {
		ruleSpec = append(ruleSpec, "--set-mark", rule.MarkValue)
	}

	logger.Infof("ToRuleSpec: %s", strings.Join(ruleSpec, " "))

	return ruleSpec, nil
}

func FromRuleSpec(spec []string) *packetfilter.Rule {
	rule := &packetfilter.Rule{}

	length := len(spec)
	i := 0

	for i < length {
		switch spec[i] {
		case "-p":
			rule.Proto, i = parseNextTerm(spec, i, parseProtocol)
		case "-s":
			rule.SrcCIDR, i = parseNextTerm(spec, i, noopParse)
		case "-d":
			rule.DestCIDR, i = parseNextTerm(spec, i, noopParse)
		case "-m":
			i = parseRuleMatch(spec, i, rule)
		case "--to-destination":
			rule.DnatCIDR, i = parseNextTerm(spec, i, noopParse)
		case "--to-source":
			rule.SnatCIDR, i = parseNextTerm(spec, i, noopParse)
		case "-i":
			rule.InInterface, i = parseNextTerm(spec, i, noopParse)
		case "-o":
			rule.OutInterface, i = parseNextTerm(spec, i, noopParse)
		case "--dport":
			rule.DPort, i = parseNextTerm(spec, i, noopParse)
		case "--set-mark":
			rule.MarkValue, i = parseNextTerm(spec, i, noopParse)
		case "-j":
			rule.Action, i = parseNextTerm(spec, i, parseAction)
			if rule.Action == packetfilter.RuleActionJump {
				rule.TargetChain = spec[i]
			}
		}

		i++
	}

	if rule.Action == packetfilter.RuleActionMss {
		rule.Proto = packetfilter.RuleProtoUndefined
	}

	return rule
}

func parseNextTerm[T any](spec []string, i int, parse func(s string) T) (T, int) {
	if i+1 >= len(spec) {
		return *new(T), i
	}

	i++

	return parse(spec[i]), i
}

func parseRuleMatch(spec []string, i int, rule *packetfilter.Rule) int {
	if i+1 >= len(spec) {
		return i
	}

	i++

	switch spec[i] {
	case "mark":
		if i+2 < len(spec) && spec[i+1] == "--mark" {
			rule.MarkValue = spec[i+2]
			i += 2
		}
	case "set":
		if i+3 < len(spec) && spec[i+1] == "--match-set" {
			if spec[i+3] == "src" {
				rule.SrcSetName = spec[i+2]
			} else if spec[i+3] == "dst" {
				rule.DestSetName = spec[i+2]
			}

			i += 3
		}
	case "tcp":
		// Parses the form: "-m", "tcp", "--tcp-flags", "SYN,RST", "SYN", "--clamp-mss-to-pmtu"
		i = parseTCPSpec(spec, i, rule)
	}

	return i
}

func parseTCPSpec(spec []string, i int, rule *packetfilter.Rule) int {
	i++
	for i < len(spec) {
		if spec[i] == "--clamp-mss-to-pmtu" {
			rule.ClampType = packetfilter.ToPMTU
			break
		} else if spec[i] == "--set-mss" {
			rule.MssValue, i = parseNextTerm(spec, i, noopParse)
			rule.ClampType = packetfilter.ToValue
			break
		} else if !strings.HasPrefix(spec[i], "--") && strings.HasPrefix(spec[i], "-") {
			i--
			break
		}

		i++
	}

	return i
}

func parseAction(s string) packetfilter.RuleAction {
	for i := 1; i < len(ruleActiontoStr); i++ {
		if s == ruleActiontoStr[i] {
			return packetfilter.RuleAction(i)
		}
	}

	return packetfilter.RuleActionJump
}

func parseProtocol(s string) packetfilter.RuleProto {
	switch s {
	case "udp":
		return packetfilter.RuleProtoUDP
	case "tcp":
		return packetfilter.RuleProtoTCP
	case "icmp":
		return packetfilter.RuleProtoICMP
	case "all":
		return packetfilter.RuleProtoAll
	default:
		return packetfilter.RuleProtoUndefined
	}
}

func noopParse(s string) string {
	return s
}
