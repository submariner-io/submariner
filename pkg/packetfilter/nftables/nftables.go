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
	"bytes"
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	k8snet "k8s.io/utils/net"
	"k8s.io/utils/ptr"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/knftables"
)

const (
	/* Single table named 'submariner' is used for nftables configuration.*/
	submarinerTable   = "submariner"
	serializedVersion = "1.0"
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

	nftFamilies = map[k8snet.IPFamily]knftables.Family{
		k8snet.IPv4: knftables.IPv4Family,
		k8snet.IPv6: knftables.IPv6Family,
	}

	logger = log.Logger{Logger: logf.Log.WithName("NFTables")}
)

type packetFilter struct {
	nftables knftables.Interface
	family   k8snet.IPFamily
}

func New(family k8snet.IPFamily) (packetfilter.Driver, error) {
	nft, err := knftables.New(nftFamilies[family], submarinerTable)
	if err != nil {
		return nil, errors.Wrapf(err, "error creating knftables for family IPv%s", family)
	}

	return NewWithNft(nft, family), nil
}

func NewWithNft(nft knftables.Interface, family k8snet.IPFamily) packetfilter.Driver {
	return &packetFilter{
		nftables: nft,
		family:   family,
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
	_, found, err := p.getNftablesRuleFromList(chain, rule)
	if err != nil {
		return err
	}

	if found {
		return nil
	}

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
		if SerializeRule(rule) == ptr.Deref(existingRule.Comment, "") {
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
			rule, err := DeserializeRule(*existingRule.Comment)
			if err != nil {
				return nil, err
			}

			rules = append(rules, rule)
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
	ruleSpec := toNftRuleSpec(rule)

	knftRule := knftables.Rule{
		Chain:   chain,
		Rule:    ruleSpec,
		Comment: ptr.To(SerializeRule(rule)),
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

func protoToRuleSpec(ruleSpec []string, proto packetfilter.RuleProto, dPort string) []string {
	switch proto {
	case packetfilter.RuleProtoUDP:
		ruleSpec = append(ruleSpec, "ip", "protocol", "udp")
		if dPort != "" {
			ruleSpec = append(ruleSpec, "udp", "dport", dPort)
		}
	case packetfilter.RuleProtoTCP:
		ruleSpec = append(ruleSpec, "ip", "protocol", "tcp")
		if dPort != "" {
			ruleSpec = append(ruleSpec, "tcp", "dport", dPort)
		}
	case packetfilter.RuleProtoICMP:
		ruleSpec = append(ruleSpec, "ip", "protocol", "icmp")
	case packetfilter.RuleProtoAll:
	case packetfilter.RuleProtoUndefined:
	}

	return ruleSpec
}

func mssClampToRuleSpec(ruleSpec []string, clampType packetfilter.MssClampType, mssValue string) []string {
	switch clampType {
	case packetfilter.UndefinedMSS:
	case packetfilter.ToPMTU:
		ruleSpec = append(ruleSpec, "size", "set", "rt", "mtu")
	case packetfilter.ToValue:
		ruleSpec = append(ruleSpec, "size", "set", mssValue)
	}

	return ruleSpec
}

func setToRuleSpec(ruleSpec []string, srcSetName, destSetName string) []string {
	if srcSetName != "" {
		ruleSpec = append(ruleSpec, "ip", "saddr", "@"+srcSetName)
	}

	if destSetName != "" {
		ruleSpec = append(ruleSpec, "ip", "daddr", "@"+destSetName)
	}

	return ruleSpec
}

func toNftRuleSpec(rule *packetfilter.Rule) string {
	ruleSpec := protoToRuleSpec([]string{}, rule.Proto, rule.DPort)

	if rule.SrcCIDR != "" {
		ruleSpec = append(ruleSpec, "ip", "saddr", rule.SrcCIDR)
	}

	if rule.DestCIDR != "" {
		ruleSpec = append(ruleSpec, "ip", "daddr", rule.DestCIDR)
	}

	if rule.MarkValue != "" && rule.Action != packetfilter.RuleActionMark {
		// syntax for MarkValue '0xc0000': meta mark & 0xc0000 == 0xc0000
		ruleSpec = append(ruleSpec, "meta", "mark", "&", rule.MarkValue, "==", rule.MarkValue)
	}

	ruleSpec = setToRuleSpec(ruleSpec, rule.SrcSetName, rule.DestSetName)

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

	ruleSpec = mssClampToRuleSpec(ruleSpec, rule.ClampType, rule.MssValue)

	if rule.MarkValue != "" && rule.Action == packetfilter.RuleActionMark {
		// syntax for MarkValue '0xc0000': set mark | 0xc0000
		ruleSpec = append(ruleSpec, "set", "mark", "|", rule.MarkValue)
	}

	str := strings.Join(ruleSpec, " ")

	logger.V(log.TRACE).Infof("toNftRuleSpec: from %q to %q", rule, str)

	return str
}

func mustWriteString(buf *bytes.Buffer, s string) {
	_, err := buf.WriteString(s + " ")
	utilruntime.Must(err)
}

func mustWriteUint32(buf *bytes.Buffer, i uint32) {
	mustWriteString(buf, strconv.FormatInt(int64(i), 10))
}

func SerializeRule(rule *packetfilter.Rule) string {
	var buf bytes.Buffer

	_, err := buf.WriteString(serializedVersion)
	utilruntime.Must(err)

	mustWriteUint32(&buf, uint32(rule.Action))
	mustWriteUint32(&buf, uint32(rule.ClampType))
	mustWriteUint32(&buf, uint32(rule.Proto))

	mustWriteString(&buf, rule.SrcSetName)
	mustWriteString(&buf, rule.DestSetName)
	mustWriteString(&buf, rule.SrcCIDR)
	mustWriteString(&buf, rule.DestCIDR)
	mustWriteString(&buf, rule.SnatCIDR)
	mustWriteString(&buf, rule.DnatCIDR)
	mustWriteString(&buf, rule.OutInterface)
	mustWriteString(&buf, rule.InInterface)
	mustWriteString(&buf, rule.DPort)
	mustWriteString(&buf, rule.TargetChain)
	mustWriteString(&buf, rule.MssValue)
	mustWriteString(&buf, rule.MarkValue)

	str := buf.String()

	logger.V(log.TRACE).Infof("SerializeRule: from %q to %q (%d)", rule, str, len(str))

	return str
}

func mustReadString(buf *bytes.Buffer) string {
	b, err := buf.ReadBytes(' ')
	utilruntime.Must(err)

	return string(b[0 : len(b)-1])
}

func mustReadUint32(buf *bytes.Buffer) uint32 {
	s := mustReadString(buf)
	u, err := strconv.ParseUint(s, 10, 32)
	utilruntime.Must(err)

	return uint32(u)
}

func DeserializeRule(s string) (*packetfilter.Rule, error) {
	rule := &packetfilter.Rule{}

	buf := bytes.NewBufferString(s)

	b := make([]byte, 3)

	_, err := buf.Read(b)
	if err != nil {
		return nil, errors.Wrapf(err, "error deserializing rule %q", s)
	}

	version := string(b)
	if version != serializedVersion {
		return nil, errors.Errorf("unable to deserialize rule from %q: invalid version %q", s, version)
	}

	rule.Action = packetfilter.RuleAction(mustReadUint32(buf))
	rule.ClampType = packetfilter.MssClampType(mustReadUint32(buf))
	rule.Proto = packetfilter.RuleProto(mustReadUint32(buf))

	rule.SrcSetName = mustReadString(buf)
	rule.DestSetName = mustReadString(buf)
	rule.SrcCIDR = mustReadString(buf)
	rule.DestCIDR = mustReadString(buf)
	rule.SnatCIDR = mustReadString(buf)
	rule.DnatCIDR = mustReadString(buf)
	rule.OutInterface = mustReadString(buf)
	rule.InInterface = mustReadString(buf)
	rule.DPort = mustReadString(buf)
	rule.TargetChain = mustReadString(buf)
	rule.MssValue = mustReadString(buf)
	rule.MarkValue = mustReadString(buf)

	logger.V(log.TRACE).Infof("DeserializeRule: from %q to %q", s, rule)

	return rule, nil
}
