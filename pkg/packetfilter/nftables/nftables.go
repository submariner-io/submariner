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

package nftables

import (
	"context"
	"slices"
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	"k8s.io/utils/ptr"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/knftables"
)

const (
	/* Single table named 'submariner' is used for nftables configuration.*/
	submarinerTable = "submariner"
)

var (
	iphookChainTypeToNftablesType = map[packetfilter.ChainType]knftables.BaseChainType{
		packetfilter.ChainTypeFilter: knftables.FilterType,
		packetfilter.ChainTypeRoute:  knftables.RouteType,
		packetfilter.ChainTypeNAT:    knftables.NATType,
	}

	iphookChainHookToNftablesHook = map[packetfilter.ChainHook]knftables.BaseChainHook{
		packetfilter.ChainHookPrerouting:  knftables.PreroutingHook,
		packetfilter.ChainHookInput:       knftables.InputHook,
		packetfilter.ChainHookForward:     knftables.ForwardHook,
		packetfilter.ChainHookOutput:      knftables.OutputHook,
		packetfilter.ChainHookPostrouting: knftables.PostroutingHook,
	}

	iphookChainTypeToNftablesBasicPriority = map[packetfilter.ChainType]knftables.BaseChainPriority{
		packetfilter.ChainTypeFilter: knftables.FilterPriority,
		packetfilter.ChainTypeRoute:  knftables.ManglePriority,
		packetfilter.ChainTypeNAT:    knftables.DNATPriority,
	}

	ruleActionToStr = map[packetfilter.RuleAction][]string{
		packetfilter.RuleActionAccept: {"accept"},
		packetfilter.RuleActionMss:    {"tcp", "option", "maxseg"},
		packetfilter.RuleActionMark:   {"meta", "mark"},
		packetfilter.RuleActionSNAT:   {"snat"},
		packetfilter.RuleActionDNAT:   {"dnat"},
		packetfilter.RuleActionJump:   {"jump"},
	}

	logger = log.Logger{Logger: logf.Log.WithName("NFTables")}
)

type RuleSpec []string

func (r RuleSpec) String() string {
	return strings.Join(r, " ")
}

type packetFilter struct {
	nftables knftables.Interface
}

func New() (packetfilter.Driver, error) {
	nft, err := knftables.New(knftables.IPv4Family, submarinerTable)
	if err != nil {
		return nil, errors.Wrap(err, "error creating knftables")
	}

	return NewWithNft(nft), nil
}

func NewWithNft(nft knftables.Interface) packetfilter.Driver {
	return &packetFilter{
		nftables: nft,
	}
}

func (p *packetFilter) ChainExists(_ packetfilter.TableType, chain string) (bool, error) {
	return p.chainExists(chain)
}

func (p *packetFilter) chainExists(chain string) (bool, error) {
	chainsList, err := p.nftables.List(context.TODO(), "chains")
	if err != nil && !knftables.IsNotFound(err) {
		return false, errors.Wrap(err, "error listing chains")
	}

	return slices.Contains(chainsList, chain), nil
}

func (p *packetFilter) AppendUnique(_ packetfilter.TableType, chain string, rule *packetfilter.Rule) error {
	return p.insertRuleAtPosition(chain, rule, -1)
}

func (p *packetFilter) CreateIPHookChainIfNotExists(chain *packetfilter.ChainIPHook) error {
	tx := p.newTransactionWithTable()
	chainPriority := iphookChainTypeToNftablesBasicPriority[chain.Type]

	if chain.Priority == packetfilter.ChainPriorityFirst {
		chainPriority += "-10"
	}

	tx.Add(&knftables.Chain{
		Name:     chain.Name,
		Type:     ptr.To(iphookChainTypeToNftablesType[chain.Type]),
		Hook:     ptr.To(iphookChainHookToNftablesHook[chain.Hook]),
		Priority: ptr.To(chainPriority),
	})

	err := p.nftables.Run(context.TODO(), tx)

	return errors.Wrapf(err, "error creating %q IPHook Chain", chain.Name)
}

func (p *packetFilter) CreateChainIfNotExists(_ packetfilter.TableType, chain *packetfilter.Chain) error {
	tx := p.newTransactionWithTable()
	tx.Add(&knftables.Chain{
		Name: chain.Name,
	})

	err := p.nftables.Run(context.TODO(), tx)

	return errors.Wrapf(err, "error creating chain %q", chain.Name)
}

func (p *packetFilter) newTransactionWithTable() *knftables.Transaction {
	tx := p.nftables.NewTransaction()
	tx.Add(&knftables.Table{
		Comment: ptr.To("rules for submariner"),
	})

	return tx
}

func (p *packetFilter) DeleteIPHookChain(chain *packetfilter.ChainIPHook) error {
	return p.deleteChain(chain.Name)
}

func (p *packetFilter) DeleteChain(_ packetfilter.TableType, chain string) error {
	return p.deleteChain(chain)
}

func (p *packetFilter) deleteChain(chain string) error {
	tx := p.nftables.NewTransaction()
	tx.Delete(&knftables.Chain{
		Name: chain,
	})

	err := p.nftables.Run(context.TODO(), tx)
	if knftables.IsNotFound(err) {
		return nil
	}

	return errors.Wrapf(err, "error deleting chain %q", chain)
}

func (p *packetFilter) ClearChain(_ packetfilter.TableType, chain string) error {
	tx := p.nftables.NewTransaction()
	tx.Flush(&knftables.Chain{
		Name: chain,
	})

	err := p.nftables.Run(context.TODO(), tx)
	if knftables.IsNotFound(err) {
		return nil
	}

	return errors.Wrapf(err, "error clearing chain %q", chain)
}

func (p *packetFilter) Delete(_ packetfilter.TableType, chain string, rule *packetfilter.Rule) error {
	kRule, ok, err := p.getNftablesRuleFromList(chain, rule)
	if err != nil {
		return errors.Wrapf(err, "error getting Nftables rules for chain %q", chain)
	}

	if !ok {
		return nil
	}

	tx := p.nftables.NewTransaction()
	tx.Delete(&knftables.Rule{
		Chain:  chain,
		Handle: kRule.Handle,
	})

	err = p.nftables.Run(context.TODO(), tx)
	if knftables.IsNotFound(err) {
		return nil
	}

	return errors.Wrapf(err, "error deleting rule %q from chain %q", *kRule.Comment, chain)
}

func (p *packetFilter) getNftablesRuleFromList(chain string, rule *packetfilter.Rule) (*knftables.Rule, bool, error) {
	existingRules, err := p.nftables.ListRules(context.TODO(), chain)
	if err != nil {
		return nil, false, errors.Wrapf(err, "failed list rules for chain %q", chain)
	}

	for _, existingRule := range existingRules {
		if ToRuleSpec(rule).String() == ptr.Deref(existingRule.Comment, "") {
			return existingRule, true, nil
		}
	}

	return nil, false, nil
}

func (p *packetFilter) List(_ packetfilter.TableType, chain string) ([]*packetfilter.Rule, error) {
	existingRules, err := p.nftables.ListRules(context.TODO(), chain)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to list rules of chain %q", chain)
	}

	rules := []*packetfilter.Rule{}

	// Note: since currently ListRules returns only `Comment` and `Handle` fields
	// of Rule object. When creating a new rule `Comment` will be set to RuleSpec.

	for _, existingRule := range existingRules {
		if ptr.Deref(existingRule.Comment, "") != "" {
			ruleSpec := strings.Split(*existingRule.Comment, " ")
			rules = append(rules, FromRuleSpec(ruleSpec))
		}
	}

	return rules, nil
}

func (p *packetFilter) Insert(_ packetfilter.TableType, chain string, pos int, rule *packetfilter.Rule) error {
	return p.insertRuleAtPosition(chain, rule, pos)
}

func (p *packetFilter) Append(_ packetfilter.TableType, chain string, rule *packetfilter.Rule) error {
	return p.insertRuleAtPosition(chain, rule, -1)
}

func (p *packetFilter) insertRuleAtPosition(chain string, rule *packetfilter.Rule, pos int) error {
	ruleSpec := ToRuleSpec(rule).String()

	knftRule := knftables.Rule{
		Chain:   chain,
		Rule:    ruleSpec,
		Comment: ptr.To(ruleSpec),
	}

	tx := p.newTransactionWithTable()
	if pos == 1 {
		// use Insert to prepend rule
		tx.Insert(&knftRule)
	} else {
		if pos > 1 {
			// Index is the number of a rule (counting from 0) to add this rule after
			knftRule.Index = ptr.To(pos - 2)
		}

		tx.Add(&knftRule)
	}

	err := p.nftables.Run(context.TODO(), tx)

	return errors.Wrap(err, "error inserting rule")
}

func protoToRuleSpec(ruleSpec *RuleSpec, proto packetfilter.RuleProto, dPort string) {
	switch proto {
	case packetfilter.RuleProtoUDP:
		*ruleSpec = append(*ruleSpec, "ip", "protocol", "udp")
		if dPort != "" {
			*ruleSpec = append(*ruleSpec, "udp", "dport", dPort)
		}
	case packetfilter.RuleProtoTCP:
		*ruleSpec = append(*ruleSpec, "ip", "protocol", "tcp")
		if dPort != "" {
			*ruleSpec = append(*ruleSpec, "tcp", "dport", dPort)
		}
	case packetfilter.RuleProtoICMP:
		*ruleSpec = append(*ruleSpec, "ip", "protocol", "icmp")
	case packetfilter.RuleProtoAll:
	case packetfilter.RuleProtoUndefined:
	}
}

func mssClampToRuleSpec(ruleSpec *RuleSpec, clampType packetfilter.MssClampType, mssValue string) {
	switch clampType {
	case packetfilter.UndefinedMSS:
	case packetfilter.ToPMTU:
		*ruleSpec = append(*ruleSpec, "size", "set", "rt", "mtu")
	case packetfilter.ToValue:
		*ruleSpec = append(*ruleSpec, "size", "set", mssValue)
	}
}

func setToRuleSpec(ruleSpec *RuleSpec, srcSetName, destSetName string) {
	if srcSetName != "" {
		*ruleSpec = append(*ruleSpec, "ip", "saddr", "@"+srcSetName)
	}

	if destSetName != "" {
		*ruleSpec = append(*ruleSpec, "ip", "daddr", "@"+destSetName)
	}
}

func ToRuleSpec(rule *packetfilter.Rule) RuleSpec {
	var ruleSpec RuleSpec
	protoToRuleSpec(&ruleSpec, rule.Proto, rule.DPort)

	if rule.SrcCIDR != "" {
		ruleSpec = append(ruleSpec, "ip", "saddr", rule.SrcCIDR)
	}

	if rule.DestCIDR != "" {
		ruleSpec = append(ruleSpec, "ip", "daddr", rule.DestCIDR)
	}

	if rule.MarkValue != "" && rule.Action != packetfilter.RuleActionMark {
		ruleSpec = append(ruleSpec, "mark", rule.MarkValue)
	}

	setToRuleSpec(&ruleSpec, rule.SrcSetName, rule.DestSetName)

	if rule.OutInterface != "" {
		ruleSpec = append(ruleSpec, "oifname", rule.OutInterface)
	}

	if rule.InInterface != "" {
		ruleSpec = append(ruleSpec, "iifname", rule.InInterface)
	}

	if rule.Action == packetfilter.RuleActionMss {
		ruleSpec = append(ruleSpec, "tcp", "flags", "syn / syn,rst")
	}

	ruleSpec = append(ruleSpec, "counter")
	ruleSpec = append(ruleSpec, ruleActionToStr[rule.Action]...)

	if rule.Action == packetfilter.RuleActionJump {
		ruleSpec = append(ruleSpec, rule.TargetChain)
	}

	if rule.SnatCIDR != "" {
		ruleSpec = append(ruleSpec, "to", rule.SnatCIDR)
	}

	if rule.DnatCIDR != "" {
		ruleSpec = append(ruleSpec, "to", rule.DnatCIDR)
	}

	mssClampToRuleSpec(&ruleSpec, rule.ClampType, rule.MssValue)

	if rule.MarkValue != "" && rule.Action == packetfilter.RuleActionMark {
		ruleSpec = append(ruleSpec, "set", rule.MarkValue)
	}

	logger.V(log.TRACE).Infof("ToRuleSpec: from %q to %q", rule, ruleSpec)

	return ruleSpec
}

func FromRuleSpec(spec RuleSpec) *packetfilter.Rule {
	rule := &packetfilter.Rule{}

	length := len(spec)
	i := 0

	for i < length {
		switch spec[i] {
		case "ip":
			i = parseIPMatch(spec, i, rule)
		case "iifname":
			rule.InInterface, i = parseNextTerm(spec, i, noopParse)
		case "oifname":
			rule.OutInterface, i = parseNextTerm(spec, i, noopParse)
		case "set":
			rule.MarkValue, i = parseNextTerm(spec, i, noopParse)

		case "dport":
			rule.DPort, i = parseNextTerm(spec, i, noopParse)
		case "counter":
			rule.Action, i = parseAction(spec, i)
			if rule.Action == packetfilter.RuleActionJump {
				i++
				rule.TargetChain = spec[i]
			}
		case "size":
			i = parseTCPMssClamp(spec, i, rule)
		case "to":
			if rule.Action == packetfilter.RuleActionDNAT {
				rule.DnatCIDR, i = parseNextTerm(spec, i, noopParse)
			} else {
				rule.SnatCIDR, i = parseNextTerm(spec, i, noopParse)
			}
		case "mark":
			rule.MarkValue, i = parseNextTerm(spec, i, noopParse)
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

func parseTCPMssClamp(spec []string, i int, rule *packetfilter.Rule) int {
	if i+1 >= len(spec) {
		return i
	}

	i++

	if spec[i] == "set" {
		if i+2 < len(spec) && spec[i+1] == "rt" {
			rule.ClampType = packetfilter.ToPMTU
			i += 2
		} else if i+1 < len(spec) {
			rule.ClampType = packetfilter.ToValue
			rule.MssValue = spec[i+1]
			i++
		}
	}

	return i
}

func parseIPMatch(spec []string, i int, rule *packetfilter.Rule) int {
	if i+1 >= len(spec) {
		return i
	}

	i++
	switch spec[i] {
	case "protocol":
		if i+1 < len(spec) {
			rule.Proto = parseProtocol(spec[i+1])
		}
	case "saddr":
		rule.SrcCIDR, i = parseNextTerm(spec, i, noopParse)
		if strings.HasPrefix(rule.SrcCIDR, "@") {
			rule.SrcSetName = rule.SrcCIDR[1:]
			rule.SrcCIDR = ""
		}
	case "daddr":
		rule.DestCIDR, i = parseNextTerm(spec, i, noopParse)
		if strings.HasPrefix(rule.DestCIDR, "@") {
			rule.DestSetName = rule.DestCIDR[1:]
			rule.DestCIDR = ""
		}
	}

	return i
}

func parseAction(spec []string, i int) (packetfilter.RuleAction, int) {
	i++
	if i >= len(spec) {
		return packetfilter.RuleActionJump, i - 1
	}

	for a, str := range ruleActionToStr {
		if len(str) == 1 && spec[i] == str[0] {
			return a, i
		}
	}

	if i+2 < len(spec) && spec[i] == "tcp" && spec[i+1] == "option" && spec[i+2] == "maxseg" {
		return packetfilter.RuleActionMss, i + 2
	}

	if i+1 < len(spec) && spec[i] == "meta" && spec[i+1] == "mark" {
		return packetfilter.RuleActionMark, i + 1
	}

	return packetfilter.RuleActionJump, i - 1
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
