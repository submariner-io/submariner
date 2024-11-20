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

package node_test

import (
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/submariner/pkg/node"
	corev1 "k8s.io/api/core/v1"
	v1meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	fakeK8s "k8s.io/client-go/kubernetes/fake"
)

const localNodeName = "local-node"

var _ = Describe("GetLocalNode", func() {
	t := newTestDriver()

	When("the local Node resource exists", func() {
		It("should return the resource", func() {
			Expect(node.GetLocalNode(t.client)).To(Equal(t.node))
		})
	})

	When("the local Node resource does not exist", func() {
		BeforeEach(func() {
			t.initialObjs = []runtime.Object{}
		})

		It("should return an error", func() {
			_, err := node.GetLocalNode(t.client)
			Expect(err).To(HaveOccurred())
		})
	})

	When("the local Node retrieval initially fails", func() {
		JustBeforeEach(func() {
			fake.FailOnAction(&t.client.Fake, "nodes", "get", nil, true)
		})

		It("should eventually return the resource", func() {
			Expect(node.GetLocalNode(t.client)).To(Equal(t.node))
		})
	})

	When("the NODE_NAME env var isn't set", func() {
		BeforeEach(func() {
			os.Unsetenv("NODE_NAME")
		})

		It("should return an error", func() {
			_, err := node.GetLocalNode(t.client)
			Expect(err).To(HaveOccurred())
		})
	})
})

type testDriver struct {
	client      *fakeK8s.Clientset
	node        *corev1.Node
	initialObjs []runtime.Object
}

func newTestDriver() *testDriver {
	t := &testDriver{}

	BeforeEach(func() {
		node.Retry = wait.Backoff{
			Steps:    2,
			Duration: 10 * time.Millisecond,
		}

		t.node = &corev1.Node{
			ObjectMeta: v1meta.ObjectMeta{
				Name: localNodeName,
			},
		}

		t.initialObjs = []runtime.Object{t.node}

		os.Setenv("NODE_NAME", localNodeName)
	})

	JustBeforeEach(func() {
		t.client = fakeK8s.NewClientset(t.initialObjs...)
	})

	return t
}
