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
 * Tencent is pleased to support the open source community by making TKEStack available.
 *
 * Copyright (C) 2012-2019 Tencent. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License"); you may not use
 * this file except in compliance with the License. You may obtain a copy of the
 * License at
 *
 * https://opensource.org/licenses/Apache-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
 * WARRANTIES OF ANY KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations under the License.
 */
/*
Copyright 2017 The Kubernetes Authors.

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

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/command"
	"github.com/submariner-io/admiral/pkg/log"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var _ = Interface(&runner{})

// Interface is an injectable interface for running ipset commands.  Implementations must be goroutine-safe.
type Interface interface {
	// FlushSet deletes all entries from a named set.
	FlushSet(set string) error
	// DestroySet deletes a named set.
	DestroySet(set string) error
	// DestroyAllSets deletes all sets.
	DestroyAllSets() error
	// CreateSet creates a new set.  It will ignore error when the set already exists if ignoreExistErr=true.
	CreateSet(set *IPSet, ignoreExistErr bool) error
	// AddEntry adds a new IP entry to the named set.  It will ignore error when the entry already exists if ignoreExistErr=true.
	AddIPEntry(ip string, set string, ignoreExistErr bool) error
	AddEntry(entry *Entry, set string, ignoreExistErr bool) error
	// DelEntry deletes one entry from the named set
	DelIPEntry(ip string, set string) error
	DelEntry(entry *Entry, set string) error
	// Test test if an entry exists in the named set
	TestIPEntry(ip string, set string) (bool, error)
	TestEntry(entry *Entry, set string) (bool, error)
	// ListEntries lists all the entries from a named set
	ListEntries(set string) ([]string, error)
	// ListSets list all set names from kernel
	ListSets() ([]string, error)
	// GetVersion returns the "X.Y" version string for ipset.
	GetVersion() (string, error)
	ListAllSetInfo() (string, error)
}

// IPSetCmd represents the ipset util.  We use ipset command for ipset execute.
const IPSetCmd = "ipset"

// EntryMemberPattern is the regular expression pattern of ipset member list.
// The raw output of ipset command `ipset list {set}` is similar to:
//
//	Name: foobar
//	Type: hash:ip,port
//	Revision: 2
//	Header: family inet hashsize 1024 maxelem 65536
//	Size in memory: 16592
//	References: 0
//	Members:
//	  192.168.1.2,tcp:8080
//	  192.168.1.1,udp:53
var EntryMemberPattern = "(?m)^(.*\n)*Members:\n"

// VersionPattern is the regular expression pattern of ipset version string.
// ipset version output is similar to "v6.10".
var VersionPattern = "v[0-9]+\\.[0-9]+"

type PortRange struct {
	Begin uint
	End   uint
}

func (p PortRange) String() string {
	return fmt.Sprintf("%d-%d", p.Begin, p.End)
}

// IPSet implements an Interface to an set.
type IPSet struct {
	// Name is the set name.
	Name string
	// SetType specifies the ipset type.
	SetType Type
	// HashFamily specifies the protocol family of the IP addresses to be stored in the set.
	// The default is inet, i.e IPv4.  If users want to use IPv6, they should specify inet6.
	HashFamily ProtocolFamilyType
	// HashSize specifies the hash table size of ipset.
	HashSize uint
	// MaxElem specifies the max element number of ipset.
	MaxElem uint
	// PortRange specifies the port range of bitmap:port type ipset.
	PortRange PortRange
	// TODO: add comment message for ipset
}

var logger = log.Logger{Logger: logf.Log.WithName("IPSet")}

// Entry represents a ipset entry.
type Entry struct {
	// IP is the entry's IP.  The IP address protocol corresponds to the HashFamily of IPSet.
	// All entries' IP addresses in the same ip set has same the protocol, IPv4 or IPv6.
	IP string
	// Port is the entry's Port.
	Port uint
	// Protocol is the entry's Protocol.  The protocols of entries in the same ip set are all
	// the same.  The accepted protocols are TCP and UDP.
	Protocol string
	// Net is the entry's IP network address.  Network address with zero prefix size can NOT
	// be stored.
	Net string
	// IP2 is the entry's second IP.  IP2 may not be empty for `hash:ip,port,ip` type ip set.
	IP2 string
	// SetType is the type of ipset where the entry exists.
	SetType Type
	//  [ timeout value ] [ packets value ] [ bytes value ] [ comment string ] [ skbmark value ] [ skbprio value ] [ skbqueue value ]
	Options []string
}

// String returns the string format for ipset entry.
func (e *Entry) String() string {
	switch e.SetType {
	case HashIP:
		// Entry{192.168.1.1} -> 192.168.1.1
		return e.IP
	case HashIPPort:
		// Entry{192.168.1.1, udp, 53} -> 192.168.1.1,udp:53
		// Entry{192.168.1.2, tcp, 8080} -> 192.168.1.2,tcp:8080
		return fmt.Sprintf("%s,%s:%d", e.IP, e.Protocol, e.Port)
	case HashIPPortIP:
		// Entry{192.168.1.1, udp, 53, 10.0.0.1} -> 192.168.1.1,udp:53,10.0.0.1
		// Entry{192.168.1.2, tcp, 8080, 192.168.1.2} -> 192.168.1.2,tcp:8080,192.168.1.2
		return fmt.Sprintf("%s,%s:%d,%s", e.IP, e.Protocol, e.Port, e.IP2)
	case HashIPPortNet:
		// Entry{192.168.1.2, udp, 80, 10.0.1.0/24} -> 192.168.1.2,udp:80,10.0.1.0/24
		// Entry{192.168.2,25, tcp, 8080, 10.1.0.0/16} -> 192.168.2,25,tcp:8080,10.1.0.0/16
		return fmt.Sprintf("%s,%s:%d,%s", e.IP, e.Protocol, e.Port, e.Net)
	case HashNet:
		// Entry{udp, 10.0.1.0/24} -> 10.0.1.0/24
		return e.Net
	case HashNetPort:
		// Entry{udp, 80, 10.0.1.0/24} -> 10.0.1.0/24,udp:80
		return fmt.Sprintf("%s,%s:%d", e.Net, e.Protocol, e.Port)
	case BitmapPort:
		// Entry{53} -> 53
		// Entry{8080} -> 8080
		return strconv.FormatUint(uint64(e.Port), 10)
	}

	return ""
}

type runner struct{}

// New returns a new Interface which will exec ipset.
func New() Interface {
	return &runner{}
}

func (runner *runner) runWithOutput(args []string, errFormat string, a ...interface{}) (string, error) {
	logger.V(log.DEBUG).Infof("Running ipset %v", args)

	out, err := command.New(exec.Command(IPSetCmd, args...)).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w (%s)", fmt.Sprintf(errFormat, a...), err, out)
	}

	return string(out), nil
}

func (runner *runner) run(args []string, errFormat string, a ...interface{}) error {
	_, err := runner.runWithOutput(args, errFormat, a...)
	return err
}

// CreateSet creates a new set,  it will ignore error when the set already exists if ignoreExistErr=true.
func (runner *runner) CreateSet(set *IPSet, ignoreExistErr bool) error {
	// Setting default values if not present
	if set.HashSize == 0 {
		set.HashSize = 1024
	}

	if set.MaxElem == 0 {
		set.MaxElem = 65536
	}

	// Default protocol is IPv4
	if set.HashFamily == "" {
		set.HashFamily = ProtocolFamilyIPV4
	}

	// Default ipset type is "hash:ip,port"
	if len(set.SetType) == 0 {
		set.SetType = HashIPPort
	}

	if set.PortRange.Begin == 0 && set.PortRange.End == 0 {
		set.PortRange = DefaultPortRange
	}

	return runner.createSet(set, ignoreExistErr)
}

// If ignoreExistErr is set to true, then the -exist option of ipset will be specified, ipset ignores the error
// otherwise raised when the same set (setname and create parameters are identical) already exists.
func (runner *runner) createSet(set *IPSet, ignoreExistErr bool) error {
	args := []string{"create", set.Name, string(set.SetType)}
	if set.SetType == HashIPPortIP || set.SetType == HashIPPort {
		args = append(args,
			"family", string(set.HashFamily),
			"hashsize", strconv.FormatUint(uint64(set.HashSize), 10),
			"maxelem", strconv.FormatUint(uint64(set.MaxElem), 10),
		)
	}

	if set.SetType == BitmapPort {
		args = append(args, "range", set.PortRange.String())
	}

	if ignoreExistErr {
		args = append(args, "-exist")
	}

	return runner.run(args, "error creating set %q", set.Name)
}

// AddEntry adds a new IP entry to the named set.
// If the -exist option is specified, ipset ignores the error otherwise raised when
// the same set (setname and create parameters are identical) already exists.
func (runner *runner) AddIPEntry(ip, set string, ignoreExistErr bool) error {
	return runner.AddEntry(&Entry{
		SetType: HashIP,
		IP:      ip,
	}, set, ignoreExistErr)
}

func (runner *runner) AddEntry(entry *Entry, set string, ignoreExistErr bool) error {
	args := []string{"add"}
	if ignoreExistErr {
		args = append(args, "-exist")
	}

	args = append(args, set, entry.String())
	args = append(args, entry.Options...)

	return runner.run(args, "error adding entry %q to set %q", entry, set)
}

// DelEntry is used to delete the specified entry from the set.
func (runner *runner) DelIPEntry(ip, set string) error {
	return runner.DelEntry(&Entry{
		SetType: HashIP,
		IP:      ip,
	}, set)
}

func (runner *runner) DelEntry(entry *Entry, set string) error {
	// ipset del should not add options
	err := runner.run([]string{"del", set, entry.String()}, "error deleting entry %q from set %q", entry, set)
	if IsNotFoundError(err) {
		return nil
	}

	return err
}

// TestIPEntry is used to check whether the specified entry is in the set or not.
func (runner *runner) TestIPEntry(ip, set string) (bool, error) {
	return runner.TestEntry(&Entry{
		SetType: HashIP,
		IP:      ip,
	}, set)
}

func (runner *runner) TestEntry(entry *Entry, set string) (bool, error) {
	err := runner.run([]string{"test", set, entry.String()}, "error testing entry %q in set %q", entry, set)
	if err != nil {
		if strings.Contains(err.Error(), "is NOT in set") {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

// FlushSet deletes all entries from a named set.
func (runner *runner) FlushSet(set string) error {
	err := runner.run([]string{"flush", set}, "error flushing set %q", set)
	if IsNotFoundError(err) {
		return nil
	}

	return err
}

func (runner *runner) retryIfInUse(args []string, errFormat string, a ...interface{}) error {
	backoff := wait.Backoff{
		Duration: 100 * time.Millisecond,
		Factor:   1.0,
		Steps:    20,
	}

	//nolint:wrapcheck // No need to wrap.
	return retry.OnError(backoff, func(err error) bool {
		return strings.Contains(err.Error(), "is in use")
	}, func() error {
		return runner.run(args, errFormat, a...)
	})
}

// DestroySet is used to destroy a named set.
func (runner *runner) DestroySet(set string) error {
	err := runner.retryIfInUse([]string{"destroy", set}, "error destroying set %q", set)
	if IsNotFoundError(err) {
		return nil
	}

	return err
}

// DestroyAllSets is used to destroy all sets.
func (runner *runner) DestroyAllSets() error {
	return runner.retryIfInUse([]string{"destroy"}, "error destroying all sets")
}

// ListSets list all set names from kernel.
func (runner *runner) ListSets() ([]string, error) {
	out, err := runner.runWithOutput([]string{"list", "-n"}, "error listing all sets")
	if err != nil {
		return nil, err
	}

	return strings.Split(out, "\n"), nil
}

// ListEntries lists all the entries from a named set.
func (runner *runner) ListEntries(set string) ([]string, error) {
	if set == "" {
		return nil, fmt.Errorf("set name can't be empty")
	}

	out, err := runner.runWithOutput([]string{"list", set}, "error listing set %q", set)
	if err != nil {
		return nil, err
	}

	memberMatcher := regexp.MustCompile(EntryMemberPattern)
	list := memberMatcher.ReplaceAllString(out, "")
	strs := strings.Split(list, "\n")
	results := make([]string, 0)

	for i := range strs {
		if strs[i] != "" {
			results = append(results, strings.TrimSpace(strs[i]))
		}
	}

	return results, nil
}

func (runner *runner) ListAllSetInfo() (string, error) {
	return runner.runWithOutput([]string{"list"}, "error listing sets")
}

// GetVersion returns the version string.
func (runner *runner) GetVersion() (string, error) {
	return getIPSetVersionString()
}

// getIPSetVersionString runs "ipset --version" to get the version string  in the form of "X.Y", i.e "6.19".
func getIPSetVersionString() (string, error) {
	osCmd := exec.Command(IPSetCmd, "--version")
	osCmd.Stdin = bytes.NewReader([]byte{})
	cmd := command.New(osCmd)

	cmdBytes, err := cmd.CombinedOutput()
	if err != nil {
		return "", errors.Wrap(err, "error executing ipset command")
	}

	versionMatcher := regexp.MustCompile(VersionPattern)

	match := versionMatcher.FindStringSubmatch(string(cmdBytes))
	if match == nil {
		return "", fmt.Errorf("no ipset version found in string: %s", cmdBytes)
	}

	return match[0], nil
}

// IsNotFoundError returns true if the error indicates "not found".  It parses
// the error string looking for known values, which is imperfect but works in
// practice.
func IsNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	es := err.Error()

	if strings.Contains(es, "does not exist") {
		// set with the same name already exists
		// xref: https://github.com/Olipro/ipset/blob/master/lib/errcode.c#L32-L33
		return true
	}

	if strings.Contains(es, "element is missing") {
		// entry is missing from the set
		// xref: https://github.com/Olipro/ipset/blob/master/lib/parse.c#L1904
		// https://github.com/Olipro/ipset/blob/master/lib/parse.c#L1925
		return true
	}

	if strings.Contains(es, "cannot be deleted") && strings.Contains(es, "it's not added") {
		// Element cannot be deleted from the set: it's not added
		// xref: https://github.com/Olipro/ipset/blob/master/lib/errcode.c#L88
		return true
	}

	return false
}
