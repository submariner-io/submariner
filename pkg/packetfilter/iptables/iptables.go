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

var (
	tableTypeToStr = map[packetfilter.TableType]string{
		packetfilter.TableTypeFilter: "filter",
		packetfilter.TableTypeRoute:  "mangle",
		packetfilter.TableTypeNAT:    "nat",
	}

	ipHookChainTypeToTableType = map[packetfilter.ChainType]packetfilter.TableType{
		packetfilter.ChainTypeFilter: packetfilter.TableTypeFilter,
		packetfilter.ChainTypeRoute:  packetfilter.TableTypeRoute,
		packetfilter.ChainTypeNAT:    packetfilter.TableTypeNAT,
	}

	chainHookToStr = map[packetfilter.ChainHook]string{
		packetfilter.ChainHookPrerouting:  "PREROUTING",
		packetfilter.ChainHookInput:       "INPUT",
		packetfilter.ChainHookForward:     "FORWARD",
		packetfilter.ChainHookOutput:      "OUTPUT",
		packetfilter.ChainHookPostrouting: "POSTROUTING",
	}

	ruleActionToStr = map[packetfilter.RuleAction]string{
		packetfilter.RuleActionAccept: "ACCEPT",
		packetfilter.RuleActionMss:    "TCPMSS",
		packetfilter.RuleActionMark:   "MARK",
		packetfilter.RuleActionSNAT:   "SNAT",
		packetfilter.RuleActionDNAT:   "DNAT",
	}

	logger = log.Logger{Logger: logf.Log.WithName("IPTables")}
)

type packetFilter struct {
	ipt        *iptables.IPTables
	ipSetIface ipset.Interface
}

func New() (packetfilter.Driver, error) {
	ipt, err := iptables.New(iptables.IPFamily(iptables.ProtocolIPv4), iptables.Timeout(5))
	if err != nil {
		return nil, errors.Wrap(err, "error creating IP tables")
	}

	ipSetIface := ipset.New(utilexec.New())

	return &packetFilter{
		ipt:        ipt,
		ipSetIface: ipSetIface,
	}, nil
}

func (p *packetFilter) ChainExists(table packetfilter.TableType, chain string) (bool, error) {
	ok, err := p.ipt.ChainExists(tableTypeToStr[table], chain)
	return ok, errors.Wrapf(err, "error checking ChainExists for table %q, chain %q", tableTypeToStr[table], chain)
}

func (p *packetFilter) AppendUnique(table packetfilter.TableType, chain string, rule *packetfilter.Rule) error {
	ruleSpec := ToRuleSpec(rule)

	err := p.ipt.AppendUnique(tableTypeToStr[table], chain, ruleSpec...)
	if err != nil {
		return errors.Wrapf(err, "AppendUnique failed for table %q, chain %q, rule %q", tableTypeToStr[table], chain,
			strings.Join(ruleSpec, " "))
	}

	return nil
}

func (p *packetFilter) CreateIPHookChainIfNotExists(chain *packetfilter.ChainIPHook) error {
	tableType := ipHookChainTypeToTableType[chain.Type]
	table := tableTypeToStr[tableType]

	if err := p.createChainIfNotExists(tableType, chain.Name); err != nil {
		return err
	}

	jumpRule := chain.JumpRule
	if jumpRule == nil {
		jumpRule = &packetfilter.Rule{
			TargetChain: chain.Name,
			Action:      packetfilter.RuleActionJump,
		}
	}

	if chain.Priority == packetfilter.ChainPriorityFirst {
		ruleSpec := ToRuleSpec(jumpRule)
		if err := p.ipt.InsertUnique(table, chainHookToStr[chain.Hook], 1, ruleSpec...); err != nil {
			return errors.Wrapf(err, "error creating IP hook chain %q for table %q, InsertUnique failed for rule: %q",
				chainHookToStr[chain.Hook], table, strings.Join(ruleSpec, " "))
		}
	} else {
		if err := p.AppendUnique(tableType, chainHookToStr[chain.Hook], jumpRule); err != nil {
			return errors.Wrapf(err, "error creating IP hook chain %q for table %q", chainHookToStr[chain.Hook], table)
		}
	}

	return nil
}

func (p *packetFilter) CreateChainIfNotExists(table packetfilter.TableType, chain *packetfilter.Chain) error {
	return p.createChainIfNotExists(table, chain.Name)
}

func (p *packetFilter) DeleteIPHookChain(chain *packetfilter.ChainIPHook) error {
	tableType := ipHookChainTypeToTableType[chain.Type]
	jumpRule := chain.JumpRule

	if jumpRule == nil {
		jumpRule = &packetfilter.Rule{
			TargetChain: chain.Name,
			Action:      packetfilter.RuleActionJump,
		}
	}

	if err := p.Delete(tableType, chainHookToStr[chain.Hook], jumpRule); err != nil {
		return errors.Wrapf(err, "error deleting IP hook chain %q", chainHookToStr[chain.Hook])
	}

	if err := p.DeleteChain(tableType, chain.Name); err != nil {
		return err
	}

	return nil
}

func (p *packetFilter) DeleteChain(table packetfilter.TableType, chain string) error {
	return errors.Wrapf(p.ipt.DeleteChain(tableTypeToStr[table], chain), "error deleting chain %q from table %q", chain, tableTypeToStr[table])
}

func (p *packetFilter) ClearChain(table packetfilter.TableType, chain string) error {
	return errors.Wrapf(p.ipt.ClearChain(tableTypeToStr[table], chain), "error clearing chain %q for table %q", chain, tableTypeToStr[table])
}

func (p *packetFilter) Delete(table packetfilter.TableType, chain string, rule *packetfilter.Rule) error {
	ruleSpec := ToRuleSpec(rule)
	err := p.ipt.Delete(tableTypeToStr[table], chain, ruleSpec...)

	var iptError *iptables.Error

	ok := errors.As(err, &iptError)
	if ok && iptError.IsNotExist() {
		return nil
	}

	if err != nil {
		return errors.Wrapf(err, "error deleting rule %q from table %q, chain %q", strings.Join(ruleSpec, " "),
			tableTypeToStr[table], chain)
	}

	return nil
}

func (p *packetFilter) List(table packetfilter.TableType, chain string) ([]*packetfilter.Rule, error) {
	existingRules, err := p.ipt.List(tableTypeToStr[table], chain)
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

func (p *packetFilter) Insert(table packetfilter.TableType, chain string, pos int, rule *packetfilter.Rule) error {
	ruleSpec := ToRuleSpec(rule)

	err := p.ipt.Insert(tableTypeToStr[table], chain, pos, ruleSpec...)
	if err != nil {
		return errors.Wrapf(err, "Insert failed for table %q, chain %q, rule %q", tableTypeToStr[table], chain,
			strings.Join(ruleSpec, " "))
	}

	return nil
}

func (p *packetFilter) Append(table packetfilter.TableType, chain string, rule *packetfilter.Rule) error {
	ruleSpec := ToRuleSpec(rule)

	err := p.ipt.Append(tableTypeToStr[table], chain, ToRuleSpec(rule)...)
	if err != nil {
		return errors.Wrapf(err, "Append failed for table %q, chain %q, rule %q", tableTypeToStr[table], chain,
			strings.Join(ruleSpec, " "))
	}

	return nil
}

func (p *packetFilter) createChainIfNotExists(tableType packetfilter.TableType, chain string) error {
	exists, err := p.ChainExists(tableType, chain)
	if err == nil && exists {
		return nil
	}

	if err != nil {
		return err
	}

	table := tableTypeToStr[tableType]

	return errors.Wrapf(p.ipt.NewChain(table, chain), "error creating IP table chain %q for table %q", table, chain)
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

func ToRuleSpec(rule *packetfilter.Rule) []string {
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

	if rule.Action == packetfilter.RuleActionJump {
		ruleSpec = append(ruleSpec, "-j", rule.TargetChain)
	} else {
		ruleSpec = append(ruleSpec, "-j", ruleActionToStr[rule.Action])
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

	logger.V(log.TRACE).Infof("ToRuleSpec: from %q to %q", rule, strings.Join(ruleSpec, " "))

	return ruleSpec
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
	for a, str := range ruleActionToStr {
		if s == str {
			return a
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
