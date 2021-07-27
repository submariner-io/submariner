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
	"github.com/submariner-io/admiral/pkg/stringset"
)

type IPTables struct {
	sync.Mutex
	chainRules               map[string]stringset.Interface
	tableChains              map[string]stringset.Interface
	failOnAppendRuleMatchers []interface{}
	failOnDeleteRuleMatchers []interface{}
}

func New() *IPTables {
	ipt := &IPTables{
		chainRules:  map[string]stringset.Interface{},
		tableChains: map[string]stringset.Interface{},
	}

	return ipt
}

func (i *IPTables) Append(table, chain string, rulespec ...string) error {
	return i.addRule(table, chain, rulespec...)
}

func (i *IPTables) AppendUnique(table, chain string, rulespec ...string) error {
	return i.addRule(table, chain, rulespec...)
}

func (i *IPTables) Insert(table, chain string, pos int, rulespec ...string) error {
	return i.addRule(table, chain, rulespec...)
}

func (i *IPTables) Delete(table, chain string, rulespec ...string) error {
	i.Lock()
	defer i.Unlock()

	err := matchRuleForError(&i.failOnDeleteRuleMatchers, rulespec...)
	if err != nil {
		return err
	}

	ruleSet := i.chainRules[table+"/"+chain]
	if ruleSet != nil {
		ruleSet.Remove(strings.Join(rulespec, " "))
	}

	return nil
}

func (i *IPTables) addRule(table, chain string, rulespec ...string) error {
	i.Lock()
	defer i.Unlock()

	err := matchRuleForError(&i.failOnAppendRuleMatchers, rulespec...)
	if err != nil {
		return err
	}

	ruleSet := i.chainRules[table+"/"+chain]
	if ruleSet == nil {
		ruleSet = stringset.New()
		i.chainRules[table+"/"+chain] = ruleSet
	}

	ruleSet.Add(strings.Join(rulespec, " "))

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

func (i *IPTables) List(table, chain string) ([]string, error) {
	return i.listRules(table, chain), nil
}

func (i *IPTables) listRules(table, chain string) []string {
	i.Lock()
	defer i.Unlock()

	rules := i.chainRules[table+"/"+chain]
	if rules != nil {
		return rules.Elements()
	}

	return []string{}
}

func (i *IPTables) ListChains(table string) ([]string, error) {
	return i.listChains(table), nil
}

func (i *IPTables) listChains(table string) []string {
	i.Lock()
	defer i.Unlock()

	chains := i.tableChains[table]
	if chains != nil {
		return chains.Elements()
	}

	return []string{}
}

func (i *IPTables) NewChain(table, chain string) error {
	i.AddChainsFor(table, chain)
	return nil
}

func (i *IPTables) ClearChain(table, chain string) error {
	i.Lock()
	defer i.Unlock()

	chainSet := i.tableChains[table]
	if chainSet != nil {
		chainSet.Remove(chain)
	}

	return nil
}

func (i *IPTables) AddChainsFor(table string, chains ...string) {
	i.Lock()
	defer i.Unlock()

	chainSet := i.tableChains[table]
	if chainSet == nil {
		chainSet = stringset.New()
		i.tableChains[table] = chainSet
	}

	chainSet.AddAll(chains...)
}

func (i *IPTables) AwaitChain(table string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		return i.listChains(table)
	}, 5).Should(ContainElement(stringOrMatcher), "IP table %q chains", table)
}

func (i *IPTables) AwaitNoChain(table string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		return i.listChains(table)
	}, 5).ShouldNot(ContainElement(stringOrMatcher), "IP table %q chains", table)
}

func (i *IPTables) AwaitRule(table, chain string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		return i.listRules(table, chain)
	}, 5).Should(ContainElement(stringOrMatcher), "Rules for IP table %q, chain %q", table, chain)
}

func (i *IPTables) AwaitNoRule(table, chain string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		return i.listRules(table, chain)
	}, 5).ShouldNot(ContainElement(stringOrMatcher), "Rules for IP table %q, chain %q", table, chain)
}

func (i *IPTables) AddFailOnAppendRuleMatcher(stringOrMatcher interface{}) {
	i.Lock()
	defer i.Unlock()

	i.failOnAppendRuleMatchers = append(i.failOnAppendRuleMatchers, stringOrMatcher)
}

func (i *IPTables) AddFailOnDeleteRuleMatcher(stringOrMatcher interface{}) {
	i.Lock()
	defer i.Unlock()

	i.failOnDeleteRuleMatchers = append(i.failOnDeleteRuleMatchers, stringOrMatcher)
}
