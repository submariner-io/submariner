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
	"sync"

	"github.com/submariner-io/admiral/pkg/stringset"
	"github.com/submariner-io/submariner/pkg/ipset"
)

type IPSet struct {
	sync.Mutex
	chainRules  map[string]stringset.Interface
	tableChains map[string]stringset.Interface
}

func New() *IPSet {
	ipSet := &IPSet{
		chainRules:  map[string]stringset.Interface{},
		tableChains: map[string]stringset.Interface{},
	}

	return ipSet
}

func (i *IPSet) CreateSet(set *ipset.IPSet, ignoreExistErr bool) error {
	// TODO, have to implement these apis for unit testing.
	return nil
}

func (i *IPSet) FlushSet(set string) error {
	return nil
}

func (i *IPSet) DestroySet(set string) error {
	return nil
}

func (i *IPSet) DestroyAllSets() error {
	return nil
}

func (i *IPSet) AddEntry(entry string, set *ipset.IPSet, ignoreExistErr bool) error {
	return nil
}

func (i *IPSet) DelEntry(entry, set string) error {
	return nil
}

func (i *IPSet) TestEntry(entry, set string) (bool, error) {
	return true, nil
}

func (i *IPSet) ListEntries(set string) ([]string, error) {
	return nil, nil
}

func (i *IPSet) ListSets() ([]string, error) {
	return nil, nil
}

func (i *IPSet) GetVersion() (string, error) {
	return "", nil
}

func (i *IPSet) AddEntryWithOptions(entry *ipset.Entry, set *ipset.IPSet, ignoreExistErr bool) error {
	return nil
}

func (i *IPSet) DelEntryWithOptions(set, entry string, options ...string) error {
	return nil
}

func (i *IPSet) ListAllSetInfo() (string, error) {
	return "", nil
}
