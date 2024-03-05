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
	"strings"
	"sync"

	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	"k8s.io/utils/set"
)

type PacketFilter struct {
	mutex                    sync.Mutex
	chainRules               map[string]set.Set[string]
	failOnAppendRuleMatchers []interface{}
	failOnDeleteRuleMatchers []interface{}

	sets                     map[string]set.Set[string]
	failOnDestroySetMatchers []interface{}
	failOnCreateSetMatchers  []interface{}
	failOnAddEntryMatchers   []interface{}
	failOnDelEntryMatchers   []interface{}
}

func New() *PacketFilter {
	pf := &PacketFilter{
		chainRules: map[string]set.Set[string]{},
		sets:       map[string]set.Set[string]{},
	}

	packetfilter.SetNewDriverFn(func() (packetfilter.Driver, error) {
		return pf, nil
	})

	return pf
}

func (i *PacketFilter) ChainExists(table packetfilter.TableType, chain string) (bool, error) {
	return i.chainExists(uint32(table), chain)
}

func (i *PacketFilter) AppendUnique(table packetfilter.TableType, chain string, rule *packetfilter.Rule) error {
	return i.addRule(table, chain, toRuleString(rule))
}

func (i *PacketFilter) CreateIPHookChainIfNotExists(chain *packetfilter.ChainIPHook) error {
	return i.createChainIfNotExists(uint32(chain.Type), chain.Name)
}

func (i *PacketFilter) CreateChainIfNotExists(table packetfilter.TableType, chain *packetfilter.Chain) error {
	return i.createChainIfNotExists(uint32(table), chain.Name)
}

func (i *PacketFilter) DeleteIPHookChain(chain *packetfilter.ChainIPHook) error {
	i.deleteChain(uint32(chain.Type), chain.Name)

	return nil
}

func (i *PacketFilter) DeleteChain(table packetfilter.TableType, chain string) error {
	i.deleteChain(uint32(table), chain)

	return nil
}

func (i *PacketFilter) ClearChain(table packetfilter.TableType, chain string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	ruleSet := i.chainRules[chainKey(uint32(table), chain)]
	if ruleSet == nil {
		return fmt.Errorf("chain %q for table %q does not exist", chain, table)
	}

	ruleSet.Clear()

	return nil
}

func (i *PacketFilter) Delete(table packetfilter.TableType, chain string, rule *packetfilter.Rule) error {
	return i.delete(table, chain, toRuleString(rule))
}

func fromRuleString(str string) *packetfilter.Rule {
	var rule packetfilter.Rule

	err := json.Unmarshal([]byte(str), &rule)
	if err != nil {
		panic(err)
	}

	return &rule
}

func toRuleString(rule *packetfilter.Rule) string {
	b, err := json.Marshal(*rule)
	if err != nil {
		panic(err)
	}

	return string(b)
}

func (i *PacketFilter) List(table packetfilter.TableType, chain string) ([]*packetfilter.Rule, error) {
	existingRules := i.listRules(table, chain)

	rules := []*packetfilter.Rule{}

	for _, existingRule := range existingRules {
		rules = append(rules, fromRuleString(existingRule))
	}

	return rules, nil
}

func (i *PacketFilter) Append(table packetfilter.TableType, chain string, rule *packetfilter.Rule) error {
	return i.addRule(table, chain, toRuleString(rule))
}

func (i *PacketFilter) Insert(table packetfilter.TableType, chain string, _ int, rule *packetfilter.Rule) error {
	return i.addRule(table, chain, toRuleString(rule))
}

func (i *PacketFilter) createChainIfNotExists(table uint32, chain string) error {
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

	ruleSet := i.chainRules[chainKey(uint32(table), chain)]
	if ruleSet != nil {
		ruleSet.Delete(rulespec)
	}

	return nil
}

func (i *PacketFilter) deleteChain(table uint32, chain string) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	delete(i.chainRules, chainKey(table, chain))
}

func (i *PacketFilter) addChainsFor(table uint32, chains ...string) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	for _, chain := range chains {
		key := chainKey(table, chain)

		ruleSet := i.chainRules[key]
		if ruleSet == nil {
			ruleSet = set.New[string]()
			i.chainRules[key] = ruleSet
		}
	}
}

func chainKey(table uint32, chain string) string {
	return fmt.Sprintf("%v/%s", table, chain)
}

func (i *PacketFilter) addRule(table packetfilter.TableType, chain, rulespec string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	err := matchRuleForError(&i.failOnAppendRuleMatchers, rulespec)
	if err != nil {
		return err
	}

	ruleSet := i.chainRules[chainKey(uint32(table), chain)]
	if ruleSet == nil {
		return fmt.Errorf("chain %q for table %q does not exist", chain, table)
	}

	ruleSet.Insert(rulespec)

	return nil
}

func (i *PacketFilter) listRules(table packetfilter.TableType, chain string) []string {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	rules := i.chainRules[chainKey(uint32(table), chain)]
	if rules != nil {
		return rules.UnsortedList()
	}

	return []string{}
}

func (i *PacketFilter) listChains(table packetfilter.TableType) []string {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	var chains []string
	tableKey := chainKey(uint32(table), "")

	for k := range i.chainRules {
		if strings.HasPrefix(k, tableKey) {
			chains = append(chains, k[len(tableKey):])
		}
	}

	return chains
}

func (i *PacketFilter) chainExists(table uint32, chain string) (bool, error) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	return i.chainRules[chainKey(table, chain)] != nil, nil
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

func (i *PacketFilter) AwaitNoRules(table packetfilter.TableType, chain string) {
	Eventually(func() []string {
		return i.listRules(table, chain)
	}, 5).Should(BeEmpty())
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
