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

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	k8snet "k8s.io/utils/net"
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

func (p *packetFilter) GetMSSClampTypes() (packetfilter.TableType, packetfilter.ChainType) {
	return packetfilter.TableTypeFilter, packetfilter.ChainTypeFilter
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
