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
	"errors"
	"strings"
	"sync"

	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	"k8s.io/utils/set"
)

type PacketFilter struct {
	mutex                    sync.Mutex
	chainRules               map[string]set.Set[string]
	tableChains              map[string]set.Set[string]
	failOnAppendRuleMatchers []interface{}
	failOnDeleteRuleMatchers []interface{}
}

func New() *PacketFilter {
	pf := &PacketFilter{
		chainRules:  map[string]set.Set[string]{},
		tableChains: map[string]set.Set[string]{},
	}

	packetfilter.SetNewDriverFn(func() (packetfilter.Driver, error) {
		return pf, nil
	})

	return pf
}

func (i *PacketFilter) Append(table, chain string, rulespec ...string) error {
	return i.addRule(table, chain, rulespec...)
}

func (i *PacketFilter) AppendUnique(table, chain string, rulespec ...string) error {
	return i.addRule(table, chain, rulespec...)
}

func (i *PacketFilter) Insert(table, chain string, _ int, rulespec ...string) error {
	return i.addRule(table, chain, rulespec...)
}

func (i *PacketFilter) Delete(table, chain string, rulespec ...string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	err := matchRuleForError(&i.failOnDeleteRuleMatchers, rulespec...)
	if err != nil {
		return err
	}

	ruleSet := i.chainRules[table+"/"+chain]
	if ruleSet != nil {
		ruleSet.Delete(strings.Join(rulespec, " "))
	}

	return nil
}

func (i *PacketFilter) addRule(table, chain string, rulespec ...string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	err := matchRuleForError(&i.failOnAppendRuleMatchers, rulespec...)
	if err != nil {
		return err
	}

	ruleSet := i.chainRules[table+"/"+chain]
	if ruleSet == nil {
		ruleSet = set.New[string]()
		i.chainRules[table+"/"+chain] = ruleSet
	}

	ruleSet.Insert(strings.Join(rulespec, " "))

	return nil
}

func matchRuleForError(matchers *[]interface{}, rulespec ...string) error {
	for i, m := range *matchers {
		matches, err := ContainElement(m).Match([]string{strings.Join(rulespec, " ")})
		Expect(err).To(Succeed())

		if matches {
			*matchers = (*matchers)[i+1:]
			return errors.New("mock IP table rule error")
		}
	}

	return nil
}

func (i *PacketFilter) List(table, chain string) ([]string, error) {
	return i.listRules(table, chain), nil
}

func (i *PacketFilter) listRules(table, chain string) []string {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	rules := i.chainRules[table+"/"+chain]
	if rules != nil {
		return rules.UnsortedList()
	}

	return []string{}
}

func (i *PacketFilter) listChains(table string) []string {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	chains := i.tableChains[table]
	if chains != nil {
		return chains.UnsortedList()
	}

	return []string{}
}

func (i *PacketFilter) NewChain(table, chain string) error {
	i.addChainsFor(table, chain)
	return nil
}

func (i *PacketFilter) ClearChain(table, chain string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	chainSet := i.tableChains[table]
	if chainSet != nil {
		chainSet.Delete(chain)
	}

	return nil
}

func (i *PacketFilter) ChainExists(table, chain string) (bool, error) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	chainSet := i.tableChains[table]
	if chainSet != nil {
		return chainSet.Has(chain), nil
	}

	return false, nil
}

func (i *PacketFilter) DeleteChain(table, chain string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	chainSet := i.tableChains[table]
	if chainSet != nil {
		chainSet.Delete(chain)
	}

	return nil
}

func (i *PacketFilter) addChainsFor(table string, chains ...string) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	chainSet := i.tableChains[table]
	if chainSet == nil {
		chainSet = set.New[string]()
		i.tableChains[table] = chainSet
	}

	chainSet.Insert(chains...)
}

func (i *PacketFilter) AwaitChain(table string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		return i.listChains(table)
	}, 5).Should(ContainElement(stringOrMatcher), "IP table %q chains", table)
}

func (i *PacketFilter) AwaitNoChain(table string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		return i.listChains(table)
	}, 5).ShouldNot(ContainElement(stringOrMatcher), "IP table %q chains", table)
}

func (i *PacketFilter) AwaitRule(table, chain string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		return i.listRules(table, chain)
	}, 5).Should(ContainElement(stringOrMatcher), "Rules for IP table %q, chain %q", table, chain)
}

func (i *PacketFilter) AwaitNoRule(table, chain string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		return i.listRules(table, chain)
	}, 5).ShouldNot(ContainElement(stringOrMatcher), "Rules for IP table %q, chain %q", table, chain)
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
