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
	"strings"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var logger = log.Logger{Logger: logf.Log.WithName("Packetfilter")}

const unknown = "???"

const (
	TableTypeFilter TableType = iota
	TableTypeRoute            // mangle
	TableTypeNAT
)

type TableType uint32

func (t TableType) String() string {
	switch t {
	case TableTypeFilter:
		return "Filter"
	case TableTypeRoute:
		return "Route"
	case TableTypeNAT:
		return "NAT"
	}

	return unknown
}

type RuleAction uint32

const (
	RuleActionJump RuleAction = iota
	RuleActionAccept
	RuleActionMss
	RuleActionMark
	RuleActionSNAT
	RuleActionDNAT
)

func (r RuleAction) String() string {
	switch r {
	case RuleActionJump:
		return "Jump"
	case RuleActionAccept:
		return "Accept"
	case RuleActionMss:
		return "MSS"
	case RuleActionMark:
		return "Mark"
	case RuleActionSNAT:
		return "SNAT"
	case RuleActionDNAT:
		return "DNAT"
	}

	return unknown
}

type RuleProto uint32

const (
	RuleProtoUndefined RuleProto = iota
	RuleProtoAll
	RuleProtoTCP
	RuleProtoUDP
	RuleProtoICMP
)

func (r RuleProto) String() string {
	switch r {
	case RuleProtoUndefined:
		return "Undefined"
	case RuleProtoAll:
		return "All"
	case RuleProtoTCP:
		return "TCP"
	case RuleProtoUDP:
		return "UDP"
	case RuleProtoICMP:
		return "ICMP"
	}

	return unknown
}

type MssClampType uint32

const (
	UndefinedMSS MssClampType = iota
	ToPMTU
	ToValue
)

func (m MssClampType) String() string {
	switch m {
	case UndefinedMSS:
		return "Undefined"
	case ToPMTU:
		return "ToPMTU"
	case ToValue:
		return "ToValue"
	}

	return unknown
}

type ChainHook uint32

const (
	ChainHookPrerouting ChainHook = iota
	ChainHookInput
	ChainHookForward
	ChainHookOutput
	ChainHookPostrouting
)

func (c ChainHook) String() string {
	switch c {
	case ChainHookPrerouting:
		return "PreRouting"
	case ChainHookInput:
		return "Input"
	case ChainHookForward:
		return "Forward"
	case ChainHookOutput:
		return "Output"
	case ChainHookPostrouting:
		return "PostRouting"
	}

	return unknown
}

type ChainPriority uint32

const (
	ChainPriorityFirst ChainPriority = iota
	ChainPriorityLast
)

func (c ChainPriority) String() string {
	switch c {
	case ChainPriorityFirst:
		return "First"
	case ChainPriorityLast:
		return "Last"
	}

	return unknown
}

type ChainType uint32

const (
	ChainTypeFilter ChainType = iota
	ChainTypeRoute            // mangle
	ChainTypeNAT
)

func (c ChainType) String() string {
	switch c {
	case ChainTypeFilter:
		return "Filter"
	case ChainTypeRoute:
		return "Route"
	case ChainTypeNAT:
		return "NAT"
	}

	return unknown
}

type ChainPolicy uint32

const (
	ChainPolicyAccept ChainPolicy = iota
	ChainPolicyDrop
)

func (c ChainPolicy) String() string {
	switch c {
	case ChainPolicyAccept:
		return "Accept"
	case ChainPolicyDrop:
		return "Drop"
	}

	return unknown
}

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

func (r *Rule) String() string {
	b := strings.Builder{}

	b.WriteString("packetfilter.Rule{Action: ")
	b.WriteString(r.Action.String())

	if r.Proto != RuleProtoUndefined {
		b.WriteString(", Proto: ")
		b.WriteString(r.Proto.String())
	}

	if r.ClampType != UndefinedMSS {
		b.WriteString(", ClampType: ")
		b.WriteString(r.ClampType.String())
	}

	if r.SrcCIDR != "" {
		b.WriteString(", SrcCIDR: ")
		b.WriteString(r.SrcCIDR)
	}

	if r.DestCIDR != "" {
		b.WriteString(", DestCIDR: ")
		b.WriteString(r.DestCIDR)
	}

	if r.SnatCIDR != "" {
		b.WriteString(", SnatCIDR: ")
		b.WriteString(r.SnatCIDR)
	}

	if r.DnatCIDR != "" {
		b.WriteString(", DnatCIDR: ")
		b.WriteString(r.DnatCIDR)
	}

	if r.SrcSetName != "" {
		b.WriteString(", SrcSetName: ")
		b.WriteString(r.SrcSetName)
	}

	if r.DestSetName != "" {
		b.WriteString(", DestSetName: ")
		b.WriteString(r.DestSetName)
	}

	if r.OutInterface != "" {
		b.WriteString(", OutInterface: ")
		b.WriteString(r.OutInterface)
	}

	if r.InInterface != "" {
		b.WriteString(", InInterface: ")
		b.WriteString(r.InInterface)
	}

	if r.DPort != "" {
		b.WriteString(", DPort: ")
		b.WriteString(r.DPort)
	}

	if r.MarkValue != "" {
		b.WriteString(", MarkValue: ")
		b.WriteString(r.MarkValue)
	}

	if r.MssValue != "" {
		b.WriteString(", MssValue: ")
		b.WriteString(r.MssValue)
	}

	if r.TargetChain != "" {
		b.WriteString(", TargetChain: ")
		b.WriteString(r.TargetChain)
	}

	b.WriteByte('}')

	return b.String()
}
