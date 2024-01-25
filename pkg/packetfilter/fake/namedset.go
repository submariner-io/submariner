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
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	"k8s.io/utils/set"
)

type namedSet struct {
	setInfo *packetfilter.SetInfo
	pfilter *PacketFilter
}

func (n *namedSet) Name() string {
	return n.setInfo.Name
}

func (n *namedSet) Flush() error {
	n.pfilter.flushSet(n.setInfo.Name)
	return nil
}

func (n *namedSet) Destroy() error {
	return n.pfilter.destroySet(n.setInfo.Name)
}

func (n *namedSet) Create(ignoreExistErr bool) error {
	return n.pfilter.createSet(n.setInfo, ignoreExistErr)
}

func (n *namedSet) AddEntry(entry string, ignoreExistErr bool) error {
	return n.pfilter.addEntry(entry, n.setInfo, ignoreExistErr)
}

func (n *namedSet) DelEntry(entry string) error {
	return n.pfilter.delEntry(entry, n.setInfo.Name)
}

func (n *namedSet) ListEntries() ([]string, error) {
	return n.pfilter.listEntries(n.setInfo.Name)
}

func (i *PacketFilter) destroySet(setName string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	err := matchForError(&i.failOnDestroySetMatchers, setName)
	if err != nil {
		return err
	}

	delete(i.sets, setName)

	return nil
}

func (i *PacketFilter) flushSet(setName string) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	entries := i.sets[setName]
	if entries != nil {
		entries.Clear()
	}
}

// CreateSet creates a new set.  It will ignore error when the set already exists if ignoreExistErr=true.
func (i *PacketFilter) createSet(nSet *packetfilter.SetInfo, ignoreExistErr bool) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	err := matchForError(&i.failOnCreateSetMatchers, nSet.Name)
	if err != nil {
		return err
	}

	if i.sets[nSet.Name] != nil {
		if ignoreExistErr {
			return nil
		}

		return fmt.Errorf("named set %q already exists", nSet.Name)
	}

	i.sets[nSet.Name] = set.New[string]()

	return nil
}

// AddEntry adds a new entry to the named set.  It will ignore error when the entry already exists if ignoreExistErr=true.
func (i *PacketFilter) addEntry(entry string, nSet *packetfilter.SetInfo, ignoreExistErr bool) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	err := matchForError(&i.failOnAddEntryMatchers, entry)
	if err != nil {
		return err
	}

	entries := i.sets[nSet.Name]
	if entries == nil {
		return fmt.Errorf("named set %q does not exist", nSet.Name)
	}

	if entries.Has(entry) && !ignoreExistErr {
		return fmt.Errorf("entry %q already exists", entry)
	}

	entries.Insert(entry)

	return nil
}

// DelEntry deletes one entry from the named set.
func (i *PacketFilter) delEntry(entry, setName string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	err := matchForError(&i.failOnDelEntryMatchers, entry)
	if err != nil {
		return err
	}

	entries := i.sets[setName]
	if entries == nil {
		return nil
	}

	entries.Delete(entry)

	return nil
}

// ListEntries lists all the entries from a named set.
func (i *PacketFilter) listEntries(setName string) ([]string, error) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	entries := i.sets[setName]
	if entries == nil {
		return nil, fmt.Errorf("named set %q does not exist", setName)
	}

	return entries.UnsortedList(), nil
}

func (i *PacketFilter) listSets() []string {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	names := []string{}

	for name := range i.sets {
		names = append(names, name)
	}

	return names
}

func (i *PacketFilter) DestroySets(nameFilter func(string) bool) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	names := []string{}

	for name := range i.sets {
		names = append(names, name)
	}

	for _, setName := range names {
		if nameFilter(setName) {
			err := matchForError(&i.failOnDestroySetMatchers, setName)
			if err != nil {
				return err
			}

			delete(i.sets, setName)
		}
	}

	return nil
}

func (i *PacketFilter) NewNamedSet(setInfo *packetfilter.SetInfo) packetfilter.NamedSet {
	return &namedSet{
		setInfo: setInfo,
		pfilter: i,
	}
}

func (i *PacketFilter) AwaitSet(stringOrMatcher interface{}) {
	Eventually(func() []string {
		s := i.listSets()
		return s
	}, 5).Should(ContainElement(stringOrMatcher))
}

func (i *PacketFilter) AwaitOneSet(stringOrMatcher interface{}) string {
	Eventually(func() []string {
		s := i.listSets()
		return s
	}, 5).Should(And(HaveLen(1), ContainElement(stringOrMatcher)))

	s := i.listSets()

	return s[0]
}

func (i *PacketFilter) AwaitSetDeleted(setName string) {
	Eventually(func() []string {
		s := i.listSets()
		return s
	}, 5).ShouldNot(ContainElement(setName))
}

func (i *PacketFilter) AwaitEntry(setName string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		e, _ := i.listEntries(setName)
		return e
	}, 5).Should(ContainElement(stringOrMatcher))
}

func (i *PacketFilter) AwaitEntryDeleted(setName string, stringOrMatcher interface{}) {
	Eventually(func() []string {
		e, _ := i.listEntries(setName)
		return e
	}, 5).ShouldNot(ContainElement(stringOrMatcher))
}

func (i *PacketFilter) AwaitNoEntry(setName string, stringOrMatcher interface{}) {
	Consistently(func() []string {
		e, _ := i.listEntries(setName)
		return e
	}, 300*time.Millisecond).ShouldNot(ContainElement(stringOrMatcher))
}

func (i *PacketFilter) AddFailOnDestroySetMatchers(stringOrMatcher interface{}) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	i.failOnDestroySetMatchers = append(i.failOnDestroySetMatchers, stringOrMatcher)
}

func (i *PacketFilter) AddFailOnCreateSetMatchers(stringOrMatcher interface{}) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	i.failOnCreateSetMatchers = append(i.failOnCreateSetMatchers, stringOrMatcher)
}

func (i *PacketFilter) AddFailOnAddEntryMatchers(stringOrMatcher interface{}) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	i.failOnAddEntryMatchers = append(i.failOnAddEntryMatchers, stringOrMatcher)
}

func (i *PacketFilter) AddFailOnDelEntryMatchers(stringOrMatcher interface{}) {
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
			return errors.New("mock NamedSet set error")
		}
	}

	return nil
}
