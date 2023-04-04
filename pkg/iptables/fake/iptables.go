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
	"github.com/submariner-io/submariner/pkg/iptables"
	"k8s.io/apimachinery/pkg/util/sets"
)

type basicType struct {
	mutex                    sync.Mutex
	chainRules               map[string]sets.Set[string]
	tableChains              map[string]sets.Set[string]
	failOnAppendRuleMatchers []interface{}
	failOnDeleteRuleMatchers []interface{}
}

type IPTables struct {
	iptables.Adapter
}

func New() *IPTables {
	return &IPTables{
		Adapter: iptables.Adapter{
			Basic: &basicType{
				chainRules:  map[string]sets.Set[string]{},
				tableChains: map[string]sets.Set[string]{},
			},
		},
	}
}

func (i *basicType) Append(table, chain string, rulespec ...string) error {
	return i.addRule(table, chain, rulespec...)
}

func (i *basicType) AppendUnique(table, chain string, rulespec ...string) error {
	return i.addRule(table, chain, rulespec...)
}

func (i *basicType) Insert(table, chain string, _ int, rulespec ...string) error {
	return i.addRule(table, chain, rulespec...)
}

func (i *basicType) Delete(table, chain string, rulespec ...string) error {
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

func (i *basicType) addRule(table, chain string, rulespec ...string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	err := matchRuleForError(&i.failOnAppendRuleMatchers, rulespec...)
	if err != nil {
		return err
	}

	ruleSet := i.chainRules[table+"/"+chain]
	if ruleSet == nil {
		ruleSet = sets.New[string]()
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

func (i *basicType) List(table, chain string) ([]string, error) {
	return i.listRules(table, chain), nil
}

func (i *basicType) listRules(table, chain string) []string {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	rules := i.chainRules[table+"/"+chain]
	if rules != nil {
		return rules.UnsortedList()
	}

	return []string{}
}

func (i *basicType) ListChains(table string) ([]string, error) {
	return i.listChains(table), nil
}

func (i *basicType) listChains(table string) []string {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	chains := i.tableChains[table]
	if chains != nil {
		return chains.UnsortedList()
	}

	return []string{}
}

func (i *basicType) NewChain(table, chain string) error {
	i.addChainsFor(table, chain)
	return nil
}

func (i *basicType) ClearChain(table, chain string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	chainSet := i.tableChains[table]
	if chainSet != nil {
		chainSet.Delete(chain)
	}

	return nil
}

func (i *basicType) ChainExists(table, chain string) (bool, error) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	chainSet := i.tableChains[table]
	if chainSet != nil {
		return chainSet.Has(chain), nil
	}

	return false, nil
}

func (i *basicType) DeleteChain(table, chain string) error {
	// TODO Implement chain deletion for testing
	return nil
}

func (i *basicType) addChainsFor(table string, chains ...string) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	chainSet := i.tableChains[table]
	if chainSet == nil {
		chainSet = sets.New[string]()
		i.tableChains[table] = chainSet
	}

	chainSet.Insert(chains...)
}

func (i *IPTables) AwaitChain(table string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		return i.basic().listChains(table)
	}, 5).Should(ContainElement(stringOrMatcher), "IP table %q chains", table)
}

func (i *IPTables) AwaitNoChain(table string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		return i.basic().listChains(table)
	}, 5).ShouldNot(ContainElement(stringOrMatcher), "IP table %q chains", table)
}

func (i *IPTables) AwaitRule(table, chain string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		return i.basic().listRules(table, chain)
	}, 5).Should(ContainElement(stringOrMatcher), "Rules for IP table %q, chain %q", table, chain)
}

func (i *IPTables) AwaitNoRule(table, chain string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		return i.basic().listRules(table, chain)
	}, 5).ShouldNot(ContainElement(stringOrMatcher), "Rules for IP table %q, chain %q", table, chain)
}

func (i *IPTables) AddFailOnAppendRuleMatcher(stringOrMatcher interface{}) {
	i.basic().mutex.Lock()
	defer i.basic().mutex.Unlock()

	i.basic().failOnAppendRuleMatchers = append(i.basic().failOnAppendRuleMatchers, stringOrMatcher)
}

func (i *IPTables) AddFailOnDeleteRuleMatcher(stringOrMatcher interface{}) {
	i.basic().mutex.Lock()
	defer i.basic().mutex.Unlock()

	i.basic().failOnDeleteRuleMatchers = append(i.basic().failOnDeleteRuleMatchers, stringOrMatcher)
}

func (i *IPTables) basic() *basicType {
	return i.Adapter.Basic.(*basicType)
}
