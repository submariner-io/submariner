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

package ipset_test

import (
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	fakecommand "github.com/submariner-io/admiral/pkg/command/fake"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	"github.com/submariner-io/submariner/pkg/ipset"
	"k8s.io/utils/set"
)

const (
	testSet      = "test-set"
	entryIP      = "192.168.1.1"
	invalidValue = "bogus"
)

var IPSetPathMatcher = HaveSuffix(ipset.IPSetCmd)

func TestIPSet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "IP Set Suite")
}

var _ = BeforeSuite(func() {
	kzerolog.InitK8sLogging()
})

var _ = Describe("CreateSet", func() {
	t := newTestDriver()

	Context("with default settings", func() {
		It("should invoke the correct command", func() {
			err := t.ipSet.CreateSet(&ipset.IPSet{
				Name: testSet,
			}, false)
			Expect(err).To(Succeed())
			t.cmdExecutor.AwaitCommand(IPSetPathMatcher, "create", testSet, "family", string(ipset.ProtocolFamilyIPV4))
		})
	})

	Context("with type HashIPPortIP", func() {
		It("should invoke the correct command", func() {
			err := t.ipSet.CreateSet(&ipset.IPSet{
				Name:       testSet,
				SetType:    ipset.HashIPPortIP,
				HashFamily: ipset.ProtocolFamilyIPV6,
				HashSize:   123,
				MaxElem:    999,
			}, false)
			Expect(err).To(Succeed())
			t.cmdExecutor.AwaitCommand(IPSetPathMatcher, "create", testSet, string(ipset.HashIPPortIP),
				"family", string(ipset.ProtocolFamilyIPV6), "hashsize", "123", "maxelem", "999")
		})
	})

	Context("with type BitmapPort", func() {
		It("should invoke the correct command", func() {
			err := t.ipSet.CreateSet(&ipset.IPSet{
				Name:    testSet,
				SetType: ipset.BitmapPort,
			}, false)
			Expect(err).To(Succeed())
			t.cmdExecutor.AwaitCommand(IPSetPathMatcher, "create", testSet, string(ipset.BitmapPort),
				"range", ipset.DefaultPortRange.String())
		})
	})

	Context("with ignore existing set", func() {
		It("should invoke the correct command", func() {
			err := t.ipSet.CreateSet(&ipset.IPSet{
				Name: testSet,
			}, true)
			Expect(err).To(Succeed())
			t.cmdExecutor.AwaitCommand(IPSetPathMatcher, "create", testSet, "-exist")
		})
	})

	When("the command fails", func() {
		It("should return an error", func() {
			t.cmdExecutor.SetupCommandOutputWithError("Syntax error: typename '...' is unknown", errors.New("exit status 1"),
				IPSetPathMatcher, "create")
			err := t.ipSet.CreateSet(&ipset.IPSet{
				Name: testSet,
			}, false)
			Expect(err).To(HaveOccurred())
		})
	})
})

var _ = Describe("DestroySet", func() {
	t := newTestDriver()

	It("should invoke the correct command", func() {
		err := t.ipSet.DestroySet(testSet)
		Expect(err).To(Succeed())
		t.cmdExecutor.AwaitCommand(IPSetPathMatcher, "destroy", testSet)
	})

	When("the set does not exist", func() {
		It("should succeed", func() {
			t.cmdExecutor.SetupCommandOutputWithError("The set with the given name does not exist",
				errors.New("exit status 1"), IPSetPathMatcher, "destroy")
			err := t.ipSet.DestroySet(testSet)
			Expect(err).To(Succeed())
		})
	})

	When("the set is initially in use", func() {
		It("should retry and succeed", func() {
			t.cmdExecutor.SetupCommandOutputWithError("Set cannot be destroyed: it is in use by a kernel component",
				errors.New("exit status 1"), IPSetPathMatcher, "destroy")
			err := t.ipSet.DestroySet(testSet)
			Expect(err).To(Succeed())
		})
	})
})

var _ = Describe("FlushSet", func() {
	t := newTestDriver()

	It("should invoke the correct command", func() {
		err := t.ipSet.FlushSet(testSet)
		Expect(err).To(Succeed())
		t.cmdExecutor.AwaitCommand(IPSetPathMatcher, "flush", testSet)
	})

	When("the set does not exist", func() {
		It("should succeed", func() {
			t.cmdExecutor.SetupCommandOutputWithError("The set with the given name does not exist",
				errors.New("exit status 1"), IPSetPathMatcher, "flush")
			err := t.ipSet.FlushSet(testSet)
			Expect(err).To(Succeed())
		})
	})
})

var _ = Describe("DestroyAllSets", func() {
	t := newTestDriver()

	It("should invoke the correct command", func() {
		err := t.ipSet.DestroyAllSets()
		Expect(err).To(Succeed())
		t.cmdExecutor.AwaitCommand(IPSetPathMatcher, "destroy")
	})

	When("a set is initially in use", func() {
		It("should retry and succeed", func() {
			t.cmdExecutor.SetupCommandOutputWithError("Set cannot be destroyed: it is in use by a kernel component",
				errors.New("exit status 1"), IPSetPathMatcher, "destroy")
			err := t.ipSet.DestroyAllSets()
			Expect(err).To(Succeed())
		})
	})
})

var _ = Describe("ListSets", func() {
	t := newTestDriver()

	It("should return the expected set names", func() {
		t.cmdExecutor.SetupCommandStdOut("set1\nset2\nset3", IPSetPathMatcher, "list")
		sets, err := t.ipSet.ListSets()
		Expect(err).To(Succeed())
		Expect(set.New(sets...)).To(Equal(set.New("set1", "set2", "set3")))
	})

	When("the command fails", func() {
		It("should return an error", func() {
			t.cmdExecutor.SetupCommandOutputWithError("", errors.New("exit status 1"), IPSetPathMatcher, "list")
			_, err := t.ipSet.ListSets()
			Expect(err).To(HaveOccurred())
		})
	})
})

var _ = Describe("AddIPEntry", func() {
	t := newTestDriver()

	It("should invoke the correct command", func() {
		subnet := "100.66.0.0/16"
		err := t.ipSet.AddIPEntry(subnet, testSet, false)
		Expect(err).To(Succeed())
		t.cmdExecutor.AwaitCommand(IPSetPathMatcher, "add", testSet, subnet)
	})

	Context("with ignore existing entry", func() {
		It("should invoke the correct command", func() {
			err := t.ipSet.AddIPEntry(entryIP, testSet, true)
			Expect(err).To(Succeed())
			t.cmdExecutor.AwaitCommand(IPSetPathMatcher, "add", testSet, entryIP, "-exist")
		})
	})
})

var _ = Describe("AddEntry", func() {
	t := newTestDriver()

	var entry *ipset.Entry

	testSuccess := func(args func() []string) {
		It("should invoke the correct command", func() {
			err := t.ipSet.AddEntry(entry, testSet, false)
			Expect(err).To(Succeed())
			t.cmdExecutor.AwaitCommand(IPSetPathMatcher, append([]string{"add", testSet}, args()...)...)
		})
	}

	Context("with set type HashIPPort", func() {
		BeforeEach(func() {
			entry = &ipset.Entry{
				SetType:  ipset.HashIPPort,
				IP:       entryIP,
				Port:     123,
				Protocol: ipset.ProtocolTCP,
			}
		})

		testSuccess(func() []string {
			return []string{entry.IP + "," + string(entry.Protocol) + ":123"}
		})
	})

	Context("with set type HashIPPortIP", func() {
		BeforeEach(func() {
			entry = &ipset.Entry{
				SetType:  ipset.HashIPPortIP,
				IP:       entryIP,
				Port:     123,
				Protocol: ipset.ProtocolTCP,
				IP2:      "192.168.1.2",
			}
		})

		testSuccess(func() []string {
			return []string{entry.IP + "," + string(entry.Protocol) + ":123," + entry.IP2}
		})
	})

	Context("with set type HashNet", func() {
		BeforeEach(func() {
			entry = &ipset.Entry{
				SetType: ipset.HashNet,
				Net:     "10.0.1.0/24",
			}
		})

		testSuccess(func() []string {
			return []string{entry.Net}
		})
	})

	Context("with set type HashNetPort", func() {
		BeforeEach(func() {
			entry = &ipset.Entry{
				SetType:  ipset.HashNetPort,
				Protocol: ipset.ProtocolUDP,
				Port:     123,
				Net:      "10.0.1.0/24",
			}
		})

		testSuccess(func() []string {
			return []string{entry.Net + "," + string(entry.Protocol) + ":123"}
		})
	})

	Context("with set type HashIPPortNet", func() {
		BeforeEach(func() {
			entry = &ipset.Entry{
				SetType:  ipset.HashIPPortNet,
				IP:       entryIP,
				Protocol: ipset.ProtocolUDP,
				Port:     123,
				Net:      "10.0.1.0/24",
			}
		})

		testSuccess(func() []string {
			return []string{entry.IP + "," + string(entry.Protocol) + ":123," + entry.Net}
		})
	})

	Context("with set type BitmapPort", func() {
		BeforeEach(func() {
			entry = &ipset.Entry{
				SetType: ipset.BitmapPort,
				Port:    123,
			}
		})

		testSuccess(func() []string {
			return []string{"123"}
		})
	})

	When("the command fails", func() {
		It("should return an error", func() {
			t.cmdExecutor.SetupCommandOutputWithError("", errors.New("exit status 1"), IPSetPathMatcher, "add")
			err := t.ipSet.AddEntry(entry, testSet, false)
			Expect(err).To(HaveOccurred())
		})
	})
})

var _ = Describe("DelIPEntry", func() {
	t := newTestDriver()

	It("should invoke the correct command", func() {
		err := t.ipSet.DelIPEntry(entryIP, testSet)
		Expect(err).To(Succeed())
		t.cmdExecutor.AwaitCommand(IPSetPathMatcher, "del", testSet, entryIP)
	})
})

var _ = Describe("DelEntry", func() {
	t := newTestDriver()

	It("should invoke the correct command", func() {
		entry := &ipset.Entry{
			SetType:  ipset.HashIPPort,
			IP:       entryIP,
			Port:     123,
			Protocol: ipset.ProtocolTCP,
		}

		err := t.ipSet.DelEntry(entry, testSet)
		Expect(err).To(Succeed())
		t.cmdExecutor.AwaitCommand(IPSetPathMatcher, "del", testSet, entry.IP+","+string(entry.Protocol)+":123")
	})

	When("the entry does not exist", func() {
		It("should succeed", func() {
			t.cmdExecutor.SetupCommandOutputWithError("Element cannot be deleted from the set: it's not added",
				errors.New("exit status 1"), IPSetPathMatcher, "del", testSet)
			err := t.ipSet.DelEntry(&ipset.Entry{
				SetType: ipset.HashIP,
				IP:      entryIP,
			}, testSet)
			Expect(err).To(Succeed())
		})
	})

	When("the command fails", func() {
		It("should return an error", func() {
			t.cmdExecutor.SetupCommandOutputWithError("", errors.New("exit status 1"), IPSetPathMatcher, "del")
			err := t.ipSet.DelEntry(&ipset.Entry{
				SetType: ipset.HashIP,
				IP:      entryIP,
			}, testSet)
			Expect(err).To(HaveOccurred())
		})
	})
})

var _ = Describe("TestIPEntry", func() {
	t := newTestDriver()

	When("the entry exists", func() {
		It("should return true", func() {
			found, err := t.ipSet.TestIPEntry(entryIP, testSet)
			Expect(err).To(Succeed())
			Expect(found).To(BeTrue())
			t.cmdExecutor.AwaitCommand(IPSetPathMatcher, "test", testSet, entryIP)
		})
	})
})

var _ = Describe("TestEntry", func() {
	t := newTestDriver()

	When("the entry exists", func() {
		It("should return true", func() {
			entry := &ipset.Entry{
				SetType:  ipset.HashIPPort,
				IP:       entryIP,
				Port:     123,
				Protocol: ipset.ProtocolTCP,
			}

			found, err := t.ipSet.TestEntry(entry, testSet)
			Expect(err).To(Succeed())
			Expect(found).To(BeTrue())
			t.cmdExecutor.AwaitCommand(IPSetPathMatcher, "test", testSet, entry.IP+","+string(entry.Protocol)+":123")
		})
	})

	When("the entry does not exist", func() {
		It("should return false", func() {
			t.cmdExecutor.SetupCommandOutputWithError("<ip> is NOT in set test-set",
				errors.New("exit status 1"), IPSetPathMatcher, "test", testSet)

			found, err := t.ipSet.TestEntry(&ipset.Entry{
				SetType: ipset.HashIP,
				IP:      entryIP,
			}, testSet)
			Expect(err).To(Succeed())
			Expect(found).To(BeFalse())
		})
	})

	When("the command fails", func() {
		It("should return an error", func() {
			t.cmdExecutor.SetupCommandOutputWithError("", errors.New("exit status 1"), IPSetPathMatcher, "test")
			_, err := t.ipSet.TestEntry(&ipset.Entry{
				SetType: ipset.HashIP,
				IP:      entryIP,
			}, testSet)
			Expect(err).To(HaveOccurred())
		})
	})
})

const setOutput = `
Name: foobar
Type: hash:ip,port
Revision: 6
Header: family inet hashsize 1024 maxelem 65536 bucketsize 12 initval 0xd43605a0
Size in memory: 16592
References: 0
Number of entries: 2
Members:
  192.168.1.2,tcp:8080
  192.168.1.1,udp:53
`

var _ = Describe("ListEntries", func() {
	t := newTestDriver()

	It("should return the expected entries", func() {
		t.cmdExecutor.SetupCommandStdOut(setOutput, IPSetPathMatcher, "list", testSet)
		entries, err := t.ipSet.ListEntries(testSet)
		Expect(err).To(Succeed())
		Expect(entries).To(Equal([]string{"192.168.1.2,tcp:8080", "192.168.1.1,udp:53"}))
	})

	When("the input set is empty", func() {
		It("should return an error", func() {
			_, err := t.ipSet.ListEntries("")
			Expect(err).To(HaveOccurred())
		})
	})

	When("the command fails", func() {
		It("should return an error", func() {
			t.cmdExecutor.SetupCommandOutputWithError("", errors.New("exit status 1"), IPSetPathMatcher, "list", testSet)
			_, err := t.ipSet.ListEntries(testSet)
			Expect(err).To(HaveOccurred())
		})
	})
})

var _ = Describe("GetVersion", func() {
	t := newTestDriver()

	It("should return the expected version", func() {
		t.cmdExecutor.SetupCommandStdOut("ipset v7.17, protocol version: 7", IPSetPathMatcher, "--version")
		v, err := t.ipSet.GetVersion()
		Expect(err).To(Succeed())
		Expect(v).To(Equal("v7.17"))
	})

	When("the command output does not contain a version", func() {
		It("should return an error", func() {
			t.cmdExecutor.SetupCommandStdOut("does not contain a version", IPSetPathMatcher, "--version")
			_, err := t.ipSet.GetVersion()
			Expect(err).To(HaveOccurred())
		})
	})

	When("the command fails", func() {
		It("should return an error", func() {
			t.cmdExecutor.SetupCommandOutputWithError("", errors.New("exit status 1"), IPSetPathMatcher, "--version")
			_, err := t.ipSet.GetVersion()
			Expect(err).To(HaveOccurred())
		})
	})
})

type testDriver struct {
	cmdExecutor *fakecommand.Executor
	ipSet       ipset.Interface
}

func newTestDriver() *testDriver {
	t := &testDriver{}

	BeforeEach(func() {
		t.cmdExecutor = fakecommand.New()
		t.ipSet = ipset.New()
	})

	return t
}
