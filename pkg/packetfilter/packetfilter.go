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

var logger = log.Logger{Logger: logf.Log.WithName("Packetfilter")}

const (
	TableTypeFilter TableType = iota
	TableTypeRoute            // mangle
	TableTypeNAT
	TableTypeMAX
)

type TableType uint32

type RuleAction uint32

const (
	RuleActionJump RuleAction = iota
	RuleActionAccept
	RuleActionMss
	RuleActionMark
	RuleActionSNAT
	RuleActionDNAT
	RuleActionMAX
)

type RuleProto uint32

const (
	RuleProtoUndefined RuleProto = iota
	RuleProtoAll
	RuleProtoTCP
	RuleProtoUDP
	RuleProtoICMP
)

type MssClampType uint32

const (
	UndefinedMSS MssClampType = iota
	ToPMTU
	ToValue
)

type ChainHook uint32

const (
	ChainHookPrerouting ChainHook = iota
	ChainHookInput
	ChainHookForward
	ChainHookOutput
	ChainHookPostrouting
	ChainHookMAX
)

type ChainPriority uint32

const (
	ChainPriorityFirst ChainPriority = iota
	ChainPriorityLast
)

type ChainType uint32

const (
	ChainTypeFilter ChainType = iota
	ChainTypeRoute            // mangle
	ChainTypeNAT
	ChainTypeMAX
)

type ChainPolicy uint32

const (
	ChainPolicyAccept ChainType = iota
	ChainPolicyDrop
)

type Rule struct {
	DestCIDR string
	SrcCIDR  string

	SrcSetName  string
	DestSetName string

	SnatCIDR string
	DnatCIDR string

	OutInterface string
	InInterface  string
	TargetChain  string
	MssValue     string

	DPort     string
	MarkValue string
	Action    RuleAction
	Proto     RuleProto
	ClampType MssClampType
}

// Supported policy values are accept (which is the default) or drop.
type Chain struct {
	Name   string
	Policy ChainPolicy
}

type ChainIPHook struct {
	Name     string
	Type     ChainType
	Hook     ChainHook
	Priority ChainPriority
	Policy   ChainPolicy
	JumpRule *Rule
}

type SetFamily uint32

const (
	SetFamilyV4 SetFamily = iota
	SetFamilyV6
)

// named set.
type SetInfo struct {
	// Name is the set name.
	Name string
	// SetType specifies the named type.
	SetType string
	// nftables named set attached to tables.
	Table TableType
	// SetFamily specifies the protocol family of the IP addresses to be stored in the set.
	// The default is IPv4.
	Family SetFamily
}

type NamedSet interface {
	Name() string
	Flush() error
	Destroy() error
	Create(ignoreExistErr bool) error
	AddEntry(entry string, ignoreExistErr bool) error
	DelEntry(entry string) error
	ListEntries() ([]string, error)
}

type Driver interface {
	// Chains
	ChainExists(table TableType, chain string) (bool, error)
	CreateIPHookChainIfNotExists(chain *ChainIPHook) error
	CreateChainIfNotExists(table TableType, chain *Chain) error
	DeleteIPHookChain(chain *ChainIPHook) error
	DeleteChain(table TableType, chain string) error
	ClearChain(table TableType, chain string) error

	// rules
	Delete(table TableType, chain string, rule *Rule) error
	AppendUnique(table TableType, chain string, rule *Rule) error
	List(table TableType, chain string) ([]*Rule, error)
	Append(table TableType, chain string, rule *Rule) error
	Insert(table TableType, chain string, pos int, rule *Rule) error

	// named Sets.
	NewNamedSet(set *SetInfo) NamedSet
	DestroySets(nameFilter func(string) bool) error
}

type Interface interface {
	Driver
	InsertUnique(table TableType, chain string, position int, ruleSpec *Rule) error
	PrependUnique(table TableType, chain string, ruleSpec *Rule) error
	UpdateChainRules(table TableType, chain string, rules []*Rule) error
}

var newDriverFn func() (Driver, error)

func SetNewDriverFn(f func() (Driver, error)) {
	newDriverFn = f
}

type Adapter struct {
	Driver
}

func New() (Interface, error) {
	if newDriverFn == nil {
		return nil, errors.New("no driver registered")
	}

	driver, err := newDriverFn()
	if err != nil {
		return nil, errors.Wrap(err, "error creating packet filter Driver")
	}

	return &Adapter{Driver: driver}, nil
}
