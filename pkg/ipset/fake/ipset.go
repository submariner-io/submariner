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
	"fmt"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/stringset"
	"github.com/submariner-io/submariner/pkg/ipset"
)

type IPSet struct {
	mutex                    sync.Mutex
	sets                     map[string]stringset.Interface
	failOnDestroySetMatchers []interface{}
	failOnCreateSetMatchers  []interface{}
	failOnAddEntryMatchers   []interface{}
	failOnDelEntryMatchers   []interface{}
}

var _ = ipset.Interface(&IPSet{})

func New() *IPSet {
	return &IPSet{
		sets: map[string]stringset.Interface{},
	}
}

func (i *IPSet) CreateSet(set *ipset.IPSet, ignoreExistErr bool) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	err := matchForError(&i.failOnCreateSetMatchers, set.Name)
	if err != nil {
		return err
	}

	if i.sets[set.Name] != nil {
		if ignoreExistErr {
			return nil
		}

		return fmt.Errorf("IP set %q already exists", set.Name)
	}

	i.sets[set.Name] = stringset.New()

	return nil
}

func (i *IPSet) FlushSet(set string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	entries := i.sets[set]
	if entries == nil {
		return nil
	}

	entries.RemoveAll()

	return nil
}

func (i *IPSet) DestroySet(set string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	err := matchForError(&i.failOnDestroySetMatchers, set)
	if err != nil {
		return err
	}

	if i.sets[set] == nil {
		return nil
	}

	delete(i.sets, set)

	return nil
}

func (i *IPSet) DestroyAllSets() error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	i.sets = map[string]stringset.Interface{}

	return nil
}

func (i *IPSet) AddEntry(entry string, set *ipset.IPSet, ignoreExistErr bool) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	err := matchForError(&i.failOnAddEntryMatchers, entry)
	if err != nil {
		return err
	}

	entries := i.sets[set.Name]
	if entries == nil {
		return fmt.Errorf("IP set %q does not exist", set.Name)
	}

	if !entries.Add(entry) && !ignoreExistErr {
		return fmt.Errorf("entry %q already exists", entry)
	}

	return nil
}

func (i *IPSet) DelEntry(entry, set string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	err := matchForError(&i.failOnDelEntryMatchers, entry)
	if err != nil {
		return err
	}

	entries := i.sets[set]
	if entries == nil {
		return nil
	}

	entries.Remove(entry)

	return nil
}

func (i *IPSet) TestEntry(entry, set string) (bool, error) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	entries := i.sets[set]
	if entries == nil {
		return false, fmt.Errorf("IP set %q does not exist", set)
	}

	return entries.Contains(entry), nil
}

func (i *IPSet) ListEntries(set string) ([]string, error) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	entries := i.sets[set]
	if entries == nil {
		return nil, fmt.Errorf("IP set %q does not exist", set)
	}

	return entries.Elements(), nil
}

func (i *IPSet) ListSets() ([]string, error) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	sets := []string{}

	for name := range i.sets {
		sets = append(sets, name)
	}

	return sets, nil
}

func (i *IPSet) GetVersion() (string, error) {
	return "v7.6", nil
}

func (i *IPSet) AddEntryWithOptions(entry *ipset.Entry, set *ipset.IPSet, ignoreExistErr bool) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	entries := i.sets[set.Name]
	if entries == nil {
		return fmt.Errorf("IP set %q does not exist", set)
	}

	entries.Add(entry.String())

	return nil
}

func (i *IPSet) DelEntryWithOptions(set, entry string, options ...string) error {
	return i.DelEntry(set, entry)
}

func (i *IPSet) ListAllSetInfo() (string, error) {
	return "", nil
}

func (i *IPSet) AwaitSet(stringOrMatcher interface{}) {
	Eventually(func() []string {
		s, _ := i.ListSets()
		return s
	}, 5).Should(ContainElement(stringOrMatcher))
}

func (i *IPSet) AwaitOneSet(stringOrMatcher interface{}) string {
	Eventually(func() []string {
		s, _ := i.ListSets()
		return s
	}, 5).Should(And(HaveLen(1), ContainElement(stringOrMatcher)))

	s, _ := i.ListSets()

	return s[0]
}

func (i *IPSet) AwaitSetDeleted(set string) {
	Eventually(func() []string {
		s, _ := i.ListSets()
		return s
	}, 5).ShouldNot(ContainElement(set))
}

func (i *IPSet) AwaitEntry(set string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		e, _ := i.ListEntries(set)
		return e
	}, 5).Should(ContainElement(stringOrMatcher))
}

func (i *IPSet) AwaitEntryDeleted(set string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		e, _ := i.ListEntries(set)
		return e
	}, 5).ShouldNot(ContainElement(stringOrMatcher))
}

func (i *IPSet) AwaitNoEntry(set string, stringOrMatcher interface{}) {
	Consistently(func() []string {
		e, _ := i.ListEntries(set)
		return e
	}, 300*time.Millisecond).ShouldNot(ContainElement(stringOrMatcher))
}

func (i *IPSet) AddFailOnDestroySetMatchers(stringOrMatcher interface{}) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	i.failOnDestroySetMatchers = append(i.failOnDestroySetMatchers, stringOrMatcher)
}

func (i *IPSet) AddFailOnCreateSetMatchers(stringOrMatcher interface{}) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	i.failOnCreateSetMatchers = append(i.failOnCreateSetMatchers, stringOrMatcher)
}

func (i *IPSet) AddFailOnAddEntryMatchers(stringOrMatcher interface{}) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	i.failOnAddEntryMatchers = append(i.failOnAddEntryMatchers, stringOrMatcher)
}

func (i *IPSet) AddFailOnDelEntryMatchers(stringOrMatcher interface{}) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	i.failOnDelEntryMatchers = append(i.failOnDelEntryMatchers, stringOrMatcher)
}

func matchForError(matchers *[]interface{}, rulespec ...string) error {
	for i, m := range *matchers {
		matches, err := ContainElement(m).Match([]string{strings.Join(rulespec, " ")})
		Expect(err).To(Succeed())

		if matches {
			*matchers = (*matchers)[i+1:]
			return errors.New("mock IP set error")
		}
	}

	return nil
}
