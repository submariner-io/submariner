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

	ipsetgo "github.com/lrh3321/ipset-go"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/pkg/ipset"
	"k8s.io/utils/set"
)

type IPSet struct {
	mutex                    sync.Mutex
	sets                     map[string]set.Set[string]
	failOnDestroySetMatchers []interface{}
	failOnCreateSetMatchers  []interface{}
	failOnAddEntryMatchers   []interface{}
	failOnDelEntryMatchers   []interface{}
}

var _ = ipset.Interface(&IPSet{})

func New() *IPSet {
	return &IPSet{
		sets: map[string]set.Set[string]{},
	}
}

func (i *IPSet) CreateSet(ipSet *ipsetgo.Sets, ignoreExistErr bool) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	err := matchForError(&i.failOnCreateSetMatchers, ipSet.SetName)
	if err != nil {
		return err
	}

	if i.sets[ipSet.SetName] != nil {
		if ignoreExistErr {
			return nil
		}

		return fmt.Errorf("IP set %q already exists", ipSet.SetName)
	}

	i.sets[ipSet.SetName] = set.New[string]()

	return nil
}

func (i *IPSet) FlushSet(setName string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	entries := i.sets[setName]
	if entries == nil {
		return nil
	}

	entries.Clear()

	return nil
}

func (i *IPSet) DestroySet(setName string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	err := matchForError(&i.failOnDestroySetMatchers, setName)
	if err != nil {
		return err
	}

	if i.sets[setName] == nil {
		return nil
	}

	delete(i.sets, setName)

	return nil
}

func entryToString(entry *ipsetgo.Entry) string {
	// hash IP or hash net only
	if entry.CIDR > 0 {
		return fmt.Sprintf("%s/%d", entry.IP.String(), entry.CIDR)
	}

	return entry.IP.String()
}

func (i *IPSet) AddEntry(entry *ipsetgo.Entry, ipSet *ipsetgo.Sets, ignoreExistErr bool) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	entryString := entryToString(entry)

	err := matchForError(&i.failOnAddEntryMatchers, entryString)
	if err != nil {
		return err
	}

	entries := i.sets[ipSet.SetName]
	if entries == nil {
		return fmt.Errorf("IP set %q does not exist", ipSet.SetName)
	}

	if entries.Has(entryString) && !ignoreExistErr {
		return fmt.Errorf("entry %q already exists", entryString)
	}

	entries.Insert(entryString)

	return nil
}

func (i *IPSet) DelEntry(entry *ipsetgo.Entry, setName string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	entryString := entryToString(entry)

	err := matchForError(&i.failOnDelEntryMatchers, entryString)
	if err != nil {
		return err
	}

	entries := i.sets[setName]
	if entries == nil {
		return nil
	}

	entries.Delete(entryString)

	return nil
}

func (i *IPSet) ListEntries(setName string) ([]string, error) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	entries := i.sets[setName]
	if entries == nil {
		return nil, fmt.Errorf("IP set %q does not exist", setName)
	}

	return entries.UnsortedList(), nil
}

func (i *IPSet) ListSets() ([]string, error) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	names := []string{}

	for name := range i.sets {
		names = append(names, name)
	}

	return names, nil
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

func (i *IPSet) AwaitSetDeleted(setName string) {
	Eventually(func() []string {
		s, _ := i.ListSets()
		return s
	}, 5).ShouldNot(ContainElement(setName))
}

func (i *IPSet) AwaitEntry(setName string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		e, _ := i.ListEntries(setName)
		return e
	}, 5).Should(ContainElement(stringOrMatcher))
}

func (i *IPSet) AwaitEntryDeleted(setName string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		e, _ := i.ListEntries(setName)
		return e
	}, 5).ShouldNot(ContainElement(stringOrMatcher))
}

func (i *IPSet) AwaitNoEntry(setName string, stringOrMatcher interface{}) {
	Consistently(func() []string {
		e, _ := i.ListEntries(setName)
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
