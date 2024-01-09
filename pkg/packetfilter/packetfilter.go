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
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type Driver interface {
	Append(table, chain string, rulespec ...string) error
	AppendUnique(table, chain string, rulespec ...string) error
	Delete(table, chain string, rulespec ...string) error
	Insert(table, chain string, pos int, rulespec ...string) error
	List(table, chain string) ([]string, error)
	NewChain(table, chain string) error
	ChainExists(table, chain string) (bool, error)
	ClearChain(table, chain string) error
	DeleteChain(table, chain string) error
}

type Interface interface {
	Driver
	CreateChainIfNotExists(table, chain string) error
	InsertUnique(table, chain string, position int, ruleSpec []string) error
	PrependUnique(table, chain string, ruleSpec []string) error
	// UpdateChainRules ensures that the rules in the list are the ones in rules, without any preference for the order,
	// any stale rules will be removed from the chain, and any missing rules will be added.
	UpdateChainRules(table, chain string, rules [][]string) error
}

var logger = log.Logger{Logger: logf.Log.WithName("IPTables")}

var newDriverFn func() (Driver, error)

func SetNewDriverFn(f func() (Driver, error)) {
	newDriverFn = f
}

func New() (Interface, error) {
	driver, err := newDriverFn()
	if err != nil {
		return nil, errors.Wrap(err, "error creating packet filter Driver")
	}

	return &Adapter{Driver: driver}, nil
}
