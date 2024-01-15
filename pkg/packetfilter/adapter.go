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

func (a *Adapter) PrependUnique(table TableType, chain string, ruleSpec *Rule) error {
	return a.InsertUnique(table, chain, 1, ruleSpec)
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
		return errors.Wrapf(err, "error listing the rules in table %q, chain %q", table, chain)
	}

	isPresentAtRequiredPosition := false
	numOccurrences := 0

	ruleString := fmt.Sprintf("%+v", rule)

	for index, rule := range existingRules {
		if ruleString == fmt.Sprintf("%+v", rule) {
			logger.V(level.DEBUG).Infof("In %q table, rule \"%s\", exists at index %d.", table, ruleString, index)
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
			if err = a.Delete(table, chain, rule); err != nil {
				return errors.Wrapf(err, "error deleting stale rule %q", ruleString)
			}
		}
	}

	// The required rule is present only once and is at the desired location
	if numOccurrences == 1 && isPresentAtRequiredPosition {
		logger.V(level.DEBUG).Infof("In %q table, rule \"%s\", already exists.", table, ruleString)
		return nil
	} else if err := a.Insert(table, chain, position, rule); err != nil {
		return errors.Wrapf(err, "error inserting IP rule %q", ruleString)
	}

	return nil
}
