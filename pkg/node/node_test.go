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
	"context"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/submariner/pkg/node"
	corev1 "k8s.io/api/core/v1"
	v1meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakeK8s "k8s.io/client-go/kubernetes/fake"
	nodeutil "k8s.io/component-helpers/node/util"
)

const localNodeName = "local-node"

var _ = Describe("GetLocalNode", func() {
	t := newTestDriver()

	When("the local Node resource exists", func() {
		It("should return the resource", func() {
			Expect(node.GetLocalNode(context.TODO(), t.client)).To(Equal(t.node))
		})
	})

	When("the local Node resource does not exist", func() {
		BeforeEach(func() {
			t.initialObjs = []runtime.Object{}
		})

		It("should return an error", func() {
			_, err := node.GetLocalNode(context.TODO(), t.client)
			Expect(err).To(HaveOccurred())
		})
	})

	When("the local Node retrieval initially fails", func() {
		JustBeforeEach(func() {
			fake.FailOnAction(&t.client.Fake, "nodes", "get", nil, true)
		})

		It("should eventually return the resource", func() {
			Expect(node.GetLocalNode(context.TODO(), t.client)).To(Equal(t.node))
		})
	})

	When("the NODE_NAME env var isn't set", func() {
		BeforeEach(func() {
			os.Unsetenv("NODE_NAME")
		})

		It("should panic", func() {
			Expect(func() {
				_, _ = node.GetLocalNode(context.TODO(), t.client)
			}).To(Panic())
		})
	})
})

var _ = Describe("WaitForLocalNodeReady", func() {
	t := newTestDriver()

	var (
		cancel    context.CancelFunc
		completed chan struct{}
	)

	JustBeforeEach(func() {
		var ctx context.Context

		ctx, cancel = context.WithCancel(context.Background())
		completed = make(chan struct{}, 1)

		go func() {
			node.WaitForLocalNodeReady(ctx, t.client)
			close(completed)
		}()

		DeferCleanup(cancel)

		Consistently(completed).ShouldNot(BeClosed())
	})

	When("the local Node becomes ready", func() {
		It("should return", func() {
			Expect(nodeutil.SetNodeCondition(t.client, localNodeName, corev1.NodeCondition{
				Type:   corev1.NodeReady,
				Status: corev1.ConditionTrue,
			})).To(Succeed())

			Eventually(completed, 3*time.Second).Should(BeClosed())
		})
	})

	When("the context is cancelled", func() {
		It("should return", func() {
			cancel()

			Eventually(completed, 3*time.Second).Should(BeClosed())
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
		node.PollTimeout = 30 * time.Millisecond
		node.PollInterval = 10 * time.Millisecond

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
