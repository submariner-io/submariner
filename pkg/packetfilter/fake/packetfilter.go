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

package fake

import (
	"encoding/json"
	"fmt"
	"sync"

	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	"k8s.io/utils/set"
)

type PacketFilter struct {
	mutex                    sync.Mutex
	chainRules               map[string]set.Set[string]
	tableChains              map[packetfilter.TableType]set.Set[string]
	failOnAppendRuleMatchers []interface{}
	failOnDeleteRuleMatchers []interface{}

	sets                     map[string]set.Set[string]
	failOnDestroySetMatchers []interface{}
	failOnCreateSetMatchers  []interface{}
	failOnAddEntryMatchers   []interface{}
	failOnDelEntryMatchers   []interface{}
}

var (
	iphookChainTypeToTableType = [packetfilter.ChainTypeMAX]packetfilter.TableType{
		packetfilter.TableTypeFilter,
		packetfilter.TableTypeRoute,
		packetfilter.TableTypeNAT,
	}
	chainHookToStr = [packetfilter.ChainHookMAX]string{"PREROUTING", "INPUT", "FORWARD", "OUTPUT", "POSTROUTING"}
)

func New() *PacketFilter {
	pf := &PacketFilter{
		chainRules:  map[string]set.Set[string]{},
		tableChains: map[packetfilter.TableType]set.Set[string]{},
		sets:        map[string]set.Set[string]{},
	}

	packetfilter.SetNewDriverFn(func() (packetfilter.Driver, error) {
		return pf, nil
	})

	return pf
}

func (i *PacketFilter) ChainExists(table packetfilter.TableType, chain string) (bool, error) {
	return i.chainExists(table, chain)
}

func (i *PacketFilter) AppendUnique(table packetfilter.TableType, chain string, rule *packetfilter.Rule) error {
	ruleSpecStr, err := json.Marshal(*rule)
	if err != nil {
		return errors.Wrap(err, "AppendUnique failed")
	}

	return i.addRule(table, chain, string(ruleSpecStr))
}

func (i *PacketFilter) CreateIPHookChainIfNotExists(chain *packetfilter.ChainIPHook) error {
	if err := i.createChainIfNotExists(iphookChainTypeToTableType[chain.Type], chain.Name); err != nil {
		return errors.Wrapf(err, "error creating IP tables %v:%s chain", iphookChainTypeToTableType[chain.Type], chain.Name)
	}

	return nil
}

func (i *PacketFilter) CreateChainIfNotExists(table packetfilter.TableType, chain *packetfilter.Chain) error {
	if err := i.createChainIfNotExists(table, chain.Name); err != nil {
		return errors.Wrapf(err, "error creating IP tables %s chain", chain.Name)
	}

	return nil
}

func (i *PacketFilter) DeleteIPHookChain(chain *packetfilter.ChainIPHook) error {
	tableType := iphookChainTypeToTableType[chain.Type]
	jumpRule := chain.JumpRule

	if jumpRule == nil {
		jumpRule = &packetfilter.Rule{
			TargetChain: chain.Name,
			Action:      packetfilter.RuleActionJump,
		}
	}

	if err := i.Delete(tableType, chainHookToStr[chain.Hook], jumpRule); err != nil {
		return errors.Wrap(err, "error deleting Jump Rule")
	}

	i.deleteChain(tableType, chain.Name)

	return nil
}

func (i *PacketFilter) DeleteChain(table packetfilter.TableType, chain string) error {
	i.deleteChain(table, chain)

	return nil
}

func (i *PacketFilter) ClearChain(table packetfilter.TableType, chain string) error {
	i.clearChain(table, chain)

	return nil
}

func (i *PacketFilter) Delete(table packetfilter.TableType, chain string, rule *packetfilter.Rule) error {
	ruleSpecStr, err := json.Marshal(*rule)
	if err != nil {
		return errors.Wrap(err, "Delete failed")
	}

	return i.delete(table, chain, string(ruleSpecStr))
}

func fromRuleSpec(spec string) *packetfilter.Rule {
	var rule packetfilter.Rule

	err := json.Unmarshal([]byte(spec), &rule)
	if err != nil {
		return nil
	}

	return &rule
}

func (i *PacketFilter) List(table packetfilter.TableType, chain string) ([]*packetfilter.Rule, error) {
	existingRules := i.listRules(table, chain)

	rules := []*packetfilter.Rule{}

	for _, existingRule := range existingRules {
		rules = append(rules, fromRuleSpec(existingRule))
	}

	return rules, nil
}

func (i *PacketFilter) Append(table packetfilter.TableType, chain string, rule *packetfilter.Rule) error {
	ruleSpecStr, err := json.Marshal(*rule)
	if err != nil {
		return errors.Wrap(err, "Append failed")
	}

	return i.addRule(table, chain, string(ruleSpecStr))
}

func (i *PacketFilter) Insert(table packetfilter.TableType, chain string, _ int, ruleSpec *packetfilter.Rule) error {
	ruleSpecStr, err := json.Marshal(*ruleSpec)
	if err != nil {
		return errors.Wrap(err, "Insert failed")
	}

	return i.addRule(table, chain, string(ruleSpecStr))
}

func (i *PacketFilter) createChainIfNotExists(table packetfilter.TableType, chain string) error {
	exists, err := i.chainExists(table, chain)
	if err == nil && exists {
		return nil
	}

	if err != nil {
		return errors.Wrapf(err, "error finding IP table chain %q in table %q", chain, table)
	}

	i.addChainsFor(table, chain)

	return nil
}

func (i *PacketFilter) delete(table packetfilter.TableType, chain, rulespec string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	err := matchRuleForError(&i.failOnDeleteRuleMatchers, rulespec)
	if err != nil {
		return err
	}

	ruleSet := i.chainRules[fmt.Sprintf("%v/%s", table, chain)]
	if ruleSet != nil {
		ruleSet.Delete(rulespec)
	}

	return nil
}

func (i *PacketFilter) deleteChain(table packetfilter.TableType, chain string) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	chainSet := i.tableChains[table]
	if chainSet != nil {
		chainSet.Delete(chain)
	}
}

func (i *PacketFilter) clearChain(table packetfilter.TableType, chain string) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	chainSet := i.tableChains[table]
	if chainSet != nil {
		chainSet.Delete(chain)
	}
}

func (i *PacketFilter) addChainsFor(table packetfilter.TableType, chains ...string) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	chainSet := i.tableChains[table]
	if chainSet == nil {
		chainSet = set.New[string]()
		i.tableChains[table] = chainSet
	}

	chainSet.Insert(chains...)
}

func (i *PacketFilter) addRule(table packetfilter.TableType, chain, rulespec string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	err := matchRuleForError(&i.failOnAppendRuleMatchers, rulespec)
	if err != nil {
		return err
	}

	ruleSet := i.chainRules[fmt.Sprintf("%v/%s", table, chain)]
	if ruleSet == nil {
		ruleSet = set.New[string]()
		i.chainRules[fmt.Sprintf("%v/%s", table, chain)] = ruleSet
	}

	ruleSet.Insert(rulespec)

	return nil
}

func (i *PacketFilter) listRules(table packetfilter.TableType, chain string) []string {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	rules := i.chainRules[fmt.Sprintf("%v/%s", table, chain)]
	if rules != nil {
		return rules.UnsortedList()
	}

	return []string{}
}

func (i *PacketFilter) listChains(table packetfilter.TableType) []string {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	chains := i.tableChains[table]
	if chains != nil {
		return chains.UnsortedList()
	}

	return []string{}
}

func (i *PacketFilter) chainExists(table packetfilter.TableType, chain string) (bool, error) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	chainSet := i.tableChains[table]
	if chainSet != nil {
		return chainSet.Has(chain), nil
	}

	return false, nil
}

func matchRuleForError(matchers *[]interface{}, rulespec string) error {
	for i, m := range *matchers {
		matches, err := ContainElement(m).Match([]string{rulespec})
		Expect(err).To(Succeed())

		if matches {
			*matchers = (*matchers)[i+1:]
			return errors.New("mock IP table rule error")
		}
	}

	return nil
}

func (i *PacketFilter) AwaitChain(table packetfilter.TableType, stringOrMatcher interface{}) {
	Eventually(func() []string {
		return i.listChains(table)
	}, 5).Should(ContainElement(stringOrMatcher), "IP table %v chains", table)
}

func (i *PacketFilter) AwaitNoChain(table packetfilter.TableType, stringOrMatcher interface{}) {
	Eventually(func() []string {
		return i.listChains(table)
	}, 5).ShouldNot(ContainElement(stringOrMatcher), "IP table %v chains", table)
}

func (i *PacketFilter) AwaitRule(table packetfilter.TableType, chain string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		return i.listRules(table, chain)
	}, 5).Should(ContainElement(stringOrMatcher), "Rules for IP table %v, chain %q", table, chain)
}

func (i *PacketFilter) AwaitNoRule(table packetfilter.TableType, chain string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		return i.listRules(table, chain)
	}, 5).ShouldNot(ContainElement(stringOrMatcher), "Rules for IP table %v, chain %q", table, chain)
}

func (i *PacketFilter) AddFailOnAppendRuleMatcher(stringOrMatcher interface{}) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	i.failOnAppendRuleMatchers = append(i.failOnAppendRuleMatchers, stringOrMatcher)
}

func (i *PacketFilter) AddFailOnDeleteRuleMatcher(stringOrMatcher interface{}) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	i.failOnDeleteRuleMatchers = append(i.failOnDeleteRuleMatchers, stringOrMatcher)
}
