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
package ipset

type Named interface {
	Name() string
	Flush() error
	Destroy() error
	Create(ignoreExistErr bool) error
	AddEntry(entry string, ignoreExistErr bool) error
	DelEntry(entry string) error
	TestEntry(entry string) (bool, error)
	ListEntries() ([]string, error)
}

type namedType struct {
	iface Interface
	set   IPSet
}

func NewNamed(set IPSet, iface Interface) Named {
	return &namedType{
		iface: iface,
		set:   set,
	}
}

func (n *namedType) Name() string {
	return n.set.Name
}

func (n *namedType) Flush() error {
	return n.iface.FlushSet(n.set.Name)
}

func (n *namedType) Destroy() error {
	return n.iface.DestroySet(n.set.Name)
}

func (n *namedType) Create(ignoreExistErr bool) error {
	return n.iface.CreateSet(&n.set, ignoreExistErr)
}

func (n *namedType) AddEntry(entry string, ignoreExistErr bool) error {
	return n.iface.AddEntry(entry, &n.set, ignoreExistErr)
}

func (n *namedType) DelEntry(entry string) error {
	return n.iface.DelEntry(entry, n.set.Name)
}

func (n *namedType) TestEntry(entry string) (bool, error) {
	return n.iface.TestEntry(entry, n.set.Name)
}

func (n *namedType) ListEntries() ([]string, error) {
	return n.iface.ListEntries(n.set.Name)
}
