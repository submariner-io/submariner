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
/*
Copyright 2017 The Kubernetes Authors.
Copyright (C) 2012-2019 Tencent. All Rights Reserved.
*/

package ipset

import (
	"fmt"
	"syscall"

	ipsetgo "github.com/lrh3321/ipset-go"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/vishvananda/netlink/nl"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var _ = Interface(&runner{})

// Interface is an injectable interface for running ipset commands.  Implementations must be goroutine-safe.
type Interface interface {
	// FlushSet deletes all entries from a named set.
	FlushSet(set string) error
	// DestroySet deletes a named set.
	DestroySet(set string) error
	// CreateSet creates a new set.  It will ignore error when the set already exists if ignoreExistErr=true.
	CreateSet(set *ipsetgo.Sets, ignoreExistErr bool) error
	// AddEntry adds a new entry to the named set.  It will ignore error when the entry already exists if ignoreExistErr=true.
	AddEntry(entry *ipsetgo.Entry, set *ipsetgo.Sets, ignoreExistErr bool) error
	// DelEntry deletes one entry from the named set
	DelEntry(entry *ipsetgo.Entry, set string) error
	// ListEntries lists all the entries from a named set
	ListEntries(set string) ([]string, error)
	// ListSets list all set names from kernel
	ListSets() ([]string, error)
}

var logger = log.Logger{Logger: logf.Log.WithName("IPSet")}

// Validate checks if a given ipset is valid or not.
func validate(set *ipsetgo.Sets) bool {
	// Check if protocol is valid for `HashIPPort`, `HashIPPortIP` and `HashIPPortNet` type set.
	if set.TypeName == ipsetgo.TypeHashIPPort || set.TypeName == ipsetgo.TypeHashIPPortIP || set.TypeName == ipsetgo.TypeHashIPPortNet ||
		set.TypeName == ipsetgo.TypeHashNet || set.TypeName == ipsetgo.TypeHashNetPort {
		if valid := validateHashFamily(set.Family); !valid {
			return false
		}
	}
	// check set type
	if valid := validateIPSetType(set.TypeName); !valid {
		return false
	}

	return true
}

type runner struct {
	handle *ipsetgo.Handle
}

var NewFunc func() Interface

// New returns a new Interface to manipulate IP sets.
func New() (Interface, error) {
	if NewFunc != nil {
		return NewFunc(), nil
	}

	handle, err := ipsetgo.NewHandle()
	if err != nil {
		return nil, errors.Wrap(err, "error obtaining an ipset handle")
	}

	return &runner{
		handle: handle,
	}, nil
}

// CreateSet creates a new set,  it will ignore error when the set already exists if ignoreExistErr=true.
func (runner *runner) CreateSet(set *ipsetgo.Sets, ignoreExistErr bool) error {
	// Setting default values if not present
	if set.HashSize == 0 {
		set.HashSize = 1024
	}

	if set.MaxElements == 0 {
		set.MaxElements = 65536
	}

	// Default protocol is IPv4
	if set.Family == 0 {
		set.Family = nl.FAMILY_V4
	}

	// Default ipset type is "hash:ip,port"
	if set.TypeName == "" {
		set.TypeName = ipsetgo.TypeHashIPPort
	}

	if set.PortTo == 0 {
		set.PortTo = 65535
	}

	// Validate ipset before creating
	if !validate(set) {
		return fmt.Errorf("error creating ipset since it's invalid")
	}

	return runner.createSet(set, ignoreExistErr)
}

// If ignoreExistErr is set to true, then the -exist option of ipset will be specified, ipset ignores the error
// otherwise raised when the same set (setname and create parameters are identical) already exists.
func (runner *runner) createSet(set *ipsetgo.Sets, ignoreExistErr bool) error {
	options := ipsetgo.CreateOptions{}

	if set.TypeName == ipsetgo.TypeHashIPPortIP || set.TypeName == ipsetgo.TypeHashIPPort {
		options.Family = set.Family
		options.Size = set.HashSize
	}

	if set.TypeName == ipsetgo.TypeBitmapPort {
		options.PortFrom = set.PortFrom
		options.PortTo = set.PortTo
	}

	options.Replace = ignoreExistErr

	return errors.Wrapf(runner.handle.Create(set.SetName, set.TypeName, options), "error creating set %q", set.SetName)
}

// AddEntry adds a new entry to the named set.
// If the -exist option is specified, ipset ignores the error otherwise raised when
// the same set (setname and create parameters are identical) already exists.
func (runner *runner) AddEntry(entry *ipsetgo.Entry, set *ipsetgo.Sets, ignoreExistErr bool) error {
	entry.Replace = ignoreExistErr
	return errors.Wrapf(runner.handle.Add(set.SetName, entry), "error adding entry %v to set %q", entry, set.SetName)
}

// DelEntry is used to delete the specified entry from the set.
func (runner *runner) DelEntry(entry *ipsetgo.Entry, set string) error {
	err := runner.handle.Del(set, entry)
	if err != nil && !IsNotFoundError(err) {
		return errors.Wrapf(err, "error deleting entry %v from set %q", entry, set)
	}

	return nil
}

// FlushSet deletes all entries from a named set.
func (runner *runner) FlushSet(set string) error {
	err := runner.handle.Flush(set)
	if err != nil && !IsNotFoundError(err) {
		return errors.Wrapf(err, "error flushing set %q", set)
	}

	return nil
}

// DestroySet is used to destroy a named set.
func (runner *runner) DestroySet(set string) error {
	err := runner.handle.Destroy(set)
	if err != nil && !IsNotFoundError(err) {
		return errors.Wrapf(err, "error destroying set %q", set)
	}

	return nil
}

// ListSets list all set names from kernel.
func (runner *runner) ListSets() ([]string, error) {
	sets, err := runner.handle.ListAll()
	if err != nil {
		return nil, errors.Wrap(err, "error listing all sets")
	}

	names := []string{}
	for i := range sets {
		names = append(names, sets[i].SetName)
	}

	return names, nil
}

// ListEntries lists all the entries from a named set.
func (runner *runner) ListEntries(set string) ([]string, error) {
	if set == "" {
		return nil, fmt.Errorf("set name can't be empty")
	}

	sets, err := runner.handle.List(set)
	if err != nil {
		return nil, errors.Wrapf(err, "error listing set %q", set)
	}

	if sets == nil {
		return []string{}, nil
	}

	entryNames := []string{}
	for i := range sets.Entries {
		entryNames = append(entryNames, sets.Entries[i].Name)
	}

	return entryNames, nil
}

// validIPSetTypes defines the supported ip set type.
var validIPSetTypes = []string{
	ipsetgo.TypeHashIP,
	ipsetgo.TypeHashIPPort,
	ipsetgo.TypeHashIPPortIP,
	ipsetgo.TypeBitmapPort,
	ipsetgo.TypeHashIPPortNet,
	ipsetgo.TypeHashNet,
	ipsetgo.TypeHashNetPort,
}

// checks if the given ipset type is valid.
func validateIPSetType(setType string) bool {
	for _, valid := range validIPSetTypes {
		if setType == valid {
			return true
		}
	}

	logger.Errorf(nil, "Currently supported ipset types are: %v, %s is not supported", validIPSetTypes, setType)

	return false
}

// Checks if given hash family is supported in ipset.
func validateHashFamily(family uint8) bool {
	if family == nl.FAMILY_V4 || family == nl.FAMILY_V6 {
		return true
	}

	logger.Errorf(nil, "Currently supported ip set hash families are: [%d, %d], %d is not supported", nl.FAMILY_V4,
		nl.FAMILY_V6, family)

	return false
}

// IsNotFoundError returns true if the error indicates "not found".
func IsNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	var errno syscall.Errno
	if errors.As(err, &errno) && errno == syscall.ENOENT {
		// Set doesn't exist
		return true
	}

	var ipseterr ipsetgo.IPSetError
	if errors.As(err, &ipseterr) && ipseterr == ipsetgo.ErrEntryNotExist {
		// Entry doesn't exist
		return true
	}

	return false
}
