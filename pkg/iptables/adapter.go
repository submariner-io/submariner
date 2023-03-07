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

	"github.com/pkg/errors"
	level "github.com/submariner-io/admiral/pkg/log"
	"k8s.io/apimachinery/pkg/util/sets"
)

type Adapter struct {
	Basic
}

func (a *Adapter) CreateChainIfNotExists(table, chain string) error {
	exists, err := a.ChainExists(table, chain)
	if err == nil && exists {
		return nil
	}

	if err != nil {
		return errors.Wrapf(err, "error finding IP table chain %q in table %q", chain, table)
	}

	return errors.Wrap(a.NewChain(table, chain), "error creating IP table chain")
}

func (a *Adapter) InsertUnique(table, chain string, position int, ruleSpec []string) error {
	rules, err := a.List(table, chain)
	if err != nil {
		return errors.Wrapf(err, "error listing the rules in %s chain", chain)
	}

	isPresentAtRequiredPosition := false
	numOccurrences := 0

	for index, rule := range rules {
		if strings.Contains(rule, strings.Join(ruleSpec, " ")) {
			logger.V(level.DEBUG).Infof("In %s table, iptables rule \"%s\", exists at index %d.", table, strings.Join(ruleSpec, " "), index)
			numOccurrences++

			if index == position {
				isPresentAtRequiredPosition = true
			}
		}
	}

	// The required rule is present in the Chain, but either there are multiple occurrences or its
	// not at the desired location
	if numOccurrences > 1 || !isPresentAtRequiredPosition {
		for i := 0; i < numOccurrences; i++ {
			if err = a.Delete(table, chain, ruleSpec...); err != nil {
				return errors.Wrapf(err, "error deleting stale IP table rule %q", strings.Join(ruleSpec, " "))
			}
		}
	}

	// The required rule is present only once and is at the desired location
	if numOccurrences == 1 && isPresentAtRequiredPosition {
		logger.V(level.DEBUG).Infof("In %s table, iptables rule \"%s\", already exists.", table, strings.Join(ruleSpec, " "))
		return nil
	} else if err := a.Insert(table, chain, position, ruleSpec...); err != nil {
		return errors.Wrapf(err, "error inserting IP table rule %q", strings.Join(ruleSpec, " "))
	}

	return nil
}

func (a *Adapter) PrependUnique(table, chain string, ruleSpec []string) error {
	// Submariner requires certain iptable rules to be programmed at the beginning of an iptables Chain
	// so that we can preserve the sourceIP for inter-cluster traffic and avoid K8s SDN making changes
	// to the traffic.
	// In this API, we check if the required iptable rule is present at the beginning of the chain.
	// If the rule is already present and there are no stale[1] flows, we simply return. If not, we create one.
	// [1] Sometimes after we program the rule at the beginning of the chain, K8s SDN might insert some
	// new rules ahead of the rule that we programmed. In such cases, the rule that we programmed will
	// not be the first rule to hit and Submariner behavior might get affected. So, we query the rules
	// in the chain to see if the rule slipped its position, and if so, delete all such occurrences.
	// We then re-program a new rule at the beginning of the chain as required.
	return a.InsertUnique(table, chain, 1, ruleSpec)
}

func (a *Adapter) UpdateChainRules(table, chain string, rules [][]string) error {
	existingRules, err := a.List(table, chain)
	if err != nil {
		return errors.Wrapf(err, "error listing the rules in table %q, chain %q", table, chain)
	}

	ruleStrings := sets.New[string]()

	for _, existingRule := range existingRules {
		ruleSpec := strings.Split(existingRule, " ")
		if ruleSpec[0] == "-A" {
			ruleSpec = ruleSpec[2:] // remove "-A", "$chain"
			ruleStrings.Insert(strings.Trim(strings.Join(ruleSpec, " "), " "))
		}
	}

	for _, ruleSpec := range rules {
		ruleString := strings.Join(ruleSpec, " ")

		if ruleStrings.Has(ruleString) {
			ruleStrings.Delete(ruleString)
		} else {
			logger.V(level.DEBUG).Infof("Adding iptables rule in %q, %q: %q", table, chain, ruleSpec)

			if err := a.Append(table, chain, ruleSpec...); err != nil {
				return errors.Wrapf(err, "error adding rule to %v to %q, %q", ruleSpec, table, chain)
			}
		}
	}

	// remaining elements should not be there, remove them
	for _, rule := range ruleStrings.UnsortedList() {
		logger.V(level.DEBUG).Infof("Deleting stale iptables rule in %q, %q: %q", table, chain, rule)
		ruleSpec := strings.Split(rule, " ")

		if err := a.Delete(table, chain, ruleSpec...); err != nil {
			// Log and let go, as this is not a fatal error, or something that will make real harm,
			// it's more harmful to keep retrying. At this point on next update deletion of stale rules
			// will happen again
			logger.Warningf("Unable to delete iptables entry from table %q, chain %q: %q", table, chain, rule)
		}
	}

	return nil
}
