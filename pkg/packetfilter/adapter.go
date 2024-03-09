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

package packetfilter

import (
	"fmt"

	"github.com/pkg/errors"
	level "github.com/submariner-io/admiral/pkg/log"
)

func (a *Adapter) PrependUnique(table TableType, chain string, rules ...*Rule) error {
	// Submariner requires certain rules to be programmed at the beginning of a chain so that we can preserve the sourceIP for
	// inter-cluster traffic and avoid K8s SDN making changes to the traffic. Sometimes after we program a rule at the
	// beginning of the chain, K8s SDN might insert some new rules ahead of the rule that we programmed. In such cases,
	// the rule that we programmed will not be the first rule to hit and Submariner behavior might get affected. So, we
	// query the rules in the chain to see if a rule slipped its position, and, if so, delete all such occurrences and then
	// re-insert the rule at the beginning of the chain.
	existingRules, err := a.List(table, chain)
	if err != nil {
		return err
	}

	for i, rule := range rules {
		err := a.ensureRuleAtPosition(table, chain, existingRules, i+1, rule)
		if err != nil {
			return err
		}
	}

	return nil
}

func (a *Adapter) ensureRuleAtPosition(table TableType, chain string, existingRules []*Rule, position int, rule *Rule) error {
	isPresentAtRequiredPosition := false
	numOccurrences := 0

	for i, existing := range existingRules {
		if *existing == *rule {
			numOccurrences++

			if i == position-1 {
				isPresentAtRequiredPosition = true
			} else {
				logger.V(level.TRACE).Infof("Rule %q in table %q, chain %q is at position %d, not %d", rule, table, chain, i+1, position)
			}
		}
	}

	// The required rule is present in the chain, but either there are multiple occurrences or it's not at the desired position.
	if numOccurrences > 1 || !isPresentAtRequiredPosition {
		for i := 0; i < numOccurrences; i++ {
			logger.V(level.TRACE).Infof("Deleting misplaced occurrence of rule %q from table %q, chain %q", rule, table, chain)

			if err := a.Delete(table, chain, rule); err != nil {
				return err
			}
		}
	}

	// The required rule is present only once and is at the desired position.
	if numOccurrences == 1 && isPresentAtRequiredPosition {
		logger.V(level.TRACE).Infof("Rule %q already exists in table %q, chain %q at position %d - not inserting", rule, table,
			chain, position)
		return nil
	}

	logger.V(level.TRACE).Infof("Inserting rule %q in table %q, chain %q at position %d", rule, table, chain, position)

	return a.Insert(table, chain, position, rule)
}

func (a *Adapter) UpdateChainRules(table TableType, chain string, rules []*Rule) error {
	currentRules, err := a.List(table, chain)
	if err != nil {
		return errors.Wrapf(err, "error listing the rules in table %q, chain %q", table, chain)
	}

	existingRules := make(map[string]*Rule)
	for _, existingRule := range currentRules {
		existingRules[fmt.Sprintf("%+v", existingRule)] = existingRule
	}

	for _, rule := range rules {
		ruleString := fmt.Sprintf("%+v", rule)
		_, ok := existingRules[ruleString]

		if ok {
			delete(existingRules, ruleString)
		} else {
			logger.V(level.DEBUG).Infof("Adding packetfilter rule in %q, %q: %q", table, chain, ruleString)

			if err := a.Append(table, chain, rule); err != nil {
				return errors.Wrapf(err, "error adding rule %q to %q, %q", ruleString, table, chain)
			}
		}
	}

	// remaining elements should not be there, remove them
	for ruleStr, rule := range existingRules {
		logger.V(level.DEBUG).Infof("Deleting stale packetfilter rule in %q, %q: %q", table, chain, ruleStr)

		if err := a.Delete(table, chain, rule); err != nil {
			// Log and let go, as this is not a fatal error, or something that will make real harm,
			// it's more harmful to keep retrying. At this point on next update deletion of stale rules
			// will happen again
			logger.Warningf("Unable to delete packetfilter entry from table %q, chain %q: %q", table, chain, ruleStr)
		}
	}

	return nil
}

func (a *Adapter) InsertUnique(table TableType, chain string, position int, rule *Rule) error {
	existingRules, err := a.List(table, chain)
	if err != nil {
		return err
	}

	return a.ensureRuleAtPosition(table, chain, existingRules, position, rule)
}
