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
	"github.com/pkg/errors"
	"github.com/submariner-io/submariner/pkg/ipset"
	"github.com/submariner-io/submariner/pkg/packetfilter"
)

type namedSet struct {
	ipSetIface ipset.Interface
	set        ipset.IPSet
}

func (p *packetFilter) NewNamedSet(set *packetfilter.SetInfo) packetfilter.NamedSet {
	hashFamily := ipset.ProtocolFamilyIPV4

	return &namedSet{
		ipSetIface: p.ipSetIface,
		set: ipset.IPSet{
			Name:       set.Name,
			SetType:    ipset.HashNet,
			HashFamily: hashFamily,
		},
	}
}

func (n *namedSet) Name() string {
	return n.set.Name
}

func (n *namedSet) Flush() error {
	return errors.Wrap(n.ipSetIface.FlushSet(n.set.Name), "Flush failed")
}

func (n *namedSet) Destroy() error {
	return errors.Wrap(n.ipSetIface.DestroySet(n.set.Name), "Destroy failed")
}

func (n *namedSet) Create(ignoreExistErr bool) error {
	return errors.Wrap(n.ipSetIface.CreateSet(&n.set, ignoreExistErr), "Create failed")
}

func (n *namedSet) AddEntry(entry string, ignoreExistErr bool) error {
	return errors.Wrap(n.ipSetIface.AddIPEntry(entry, n.set.Name, ignoreExistErr), "AddIPEntry failed")
}

func (n *namedSet) DelEntry(entry string) error {
	return errors.Wrap(n.ipSetIface.DelIPEntry(entry, n.set.Name), "DelIPEntry failed")
}

func (n *namedSet) TestEntry(entry string) (bool, error) {
	b, err := n.ipSetIface.TestIPEntry(entry, n.set.Name)
	return b, errors.Wrap(err, "TestEntry failed")
}

func (n *namedSet) ListEntries() ([]string, error) {
	entries, err := n.ipSetIface.ListEntries(n.set.Name)
	return entries, errors.Wrap(err, "ListEntries failed")
}

func (p *packetFilter) DestroySets(nameFilter func(string) bool) error {
	namedSetList, err := p.ipSetIface.ListSets()
	if err != nil {
		return errors.Wrap(err, "error listing sets")
	}

	retErr := error(nil)

	for _, set := range namedSetList {
		if nameFilter(set) {
			err = p.ipSetIface.DestroySet(set)
			if err != nil {
				logger.Errorf(err, "Error destroying the ipset %q", set)
				retErr = err
			}
		}
	}

	return retErr
}
