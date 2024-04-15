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
	"github.com/submariner-io/submariner/pkg/ipset"
)

const testSet = "test-set"

var IPSetPathMatcher = HaveSuffix(ipset.IPSetCmd)

func TestIPSet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "IP Set Suite")
}

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
