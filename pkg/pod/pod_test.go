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

package pod_test

import (
	"context"
	"errors"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/fake"
	. "github.com/submariner-io/admiral/pkg/gomega"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/pod"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const (
	testNamespace = "test-namespace"
	testNodeName  = "test-node"
	testPodName   = "test-pod"
)

var _ = Describe("", func() {
	Describe("NewGatewayPod", testNewGatewayPod)
	Describe("SetHALabels", testSetHALabels)
})

func testNewGatewayPod() {
	t := newTestDriver()

	When("all environment variables are set", func() {
		It("should create GatewayPod successfully and set passive HA status", func(ctx context.Context) {
			gw, err := pod.NewGatewayPod(ctx, t.client)

			Expect(err).NotTo(HaveOccurred())
			Expect(gw).ToNot(BeNil())
			t.verifyPodLabels(ctx, submarinerv1.HAStatusPassive)
		})
	})

	When("SUBMARINER_NAMESPACE is not set", func() {
		BeforeEach(func() {
			Expect(os.Unsetenv("SUBMARINER_NAMESPACE")).To(Succeed())
		})

		It("should return an error", func(ctx context.Context) {
			_, err := pod.NewGatewayPod(ctx, t.client)
			Expect(err).To(HaveOccurred())
		})
	})

	When("NODE_NAME is not set", func() {
		BeforeEach(func() {
			Expect(os.Unsetenv("NODE_NAME")).To(Succeed())
		})

		It("should return an error", func(ctx context.Context) {
			_, err := pod.NewGatewayPod(ctx, t.client)
			Expect(err).To(HaveOccurred())
		})
	})

	When("POD_NAME is not set", func() {
		BeforeEach(func() {
			Expect(os.Unsetenv("POD_NAME")).To(Succeed())
		})

		It("should return an error", func(ctx context.Context) {
			_, err := pod.NewGatewayPod(ctx, t.client)
			Expect(err).To(HaveOccurred())
		})
	})

	When("setting initial passive HA status fails", func() {
		BeforeEach(func() {
			fake.FailOnAction(&t.client.Fake, "pods", "patch", apierrors.NewNotFound(schema.GroupResource{}, testPodName), false)
		})

		It("should return an error", NodeTimeout(5*time.Second), func(ctx context.Context) {
			_, err := pod.NewGatewayPod(ctx, t.client)
			Expect(err).To(HaveOccurred())
		})
	})
}

func testSetHALabels() {
	t := newTestDriver()

	var gatewayPod *pod.GatewayPod

	JustBeforeEach(func() {
		var err error

		gatewayPod, err = pod.NewGatewayPod(context.TODO(), t.client)
		Expect(err).To(Succeed())
	})

	When("setting HA status to active", func() {
		It("should update the pod labels successfully", NodeTimeout(5*time.Second), func(ctx context.Context) {
			err := gatewayPod.SetHALabels(ctx, submarinerv1.HAStatusActive)
			Expect(err).To(Succeed())
			t.verifyPodLabels(ctx, submarinerv1.HAStatusActive)
		})
	})

	When("the patch operation initially returns a conflict error", func() {
		JustBeforeEach(func() {
			attemptCount := 0
			t.client.Fake.PrependReactor("patch", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
				attemptCount++
				if attemptCount <= 2 {
					return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, testPodName, nil)
				}

				return false, nil, nil
			})
		})

		It("should retry and eventually succeed", NodeTimeout(5*time.Second), func(ctx context.Context) {
			err := gatewayPod.SetHALabels(ctx, submarinerv1.HAStatusActive)
			Expect(err).To(Succeed())
			t.verifyPodLabels(ctx, submarinerv1.HAStatusActive)
		})
	})

	DescribeTableSubtree("the patch operation initially returns error",
		func(retErr error) {
			JustBeforeEach(func() {
				fake.FailOnAction(&t.client.Fake, "pods", "patch", retErr, true)
			})

			It("should retry and eventually succeed", NodeTimeout(5*time.Second), func(ctx context.Context) {
				err := gatewayPod.SetHALabels(ctx, submarinerv1.HAStatusActive)
				Expect(err).To(Succeed())
				t.verifyPodLabels(ctx, submarinerv1.HAStatusActive)
			})
		},
		Entry("ServerTimeout", apierrors.NewServerTimeout(schema.GroupResource{}, "patch", 1)),
		Entry("InternalError", apierrors.NewInternalError(errors.New("internal error"))),
		Entry("ServiceUnavailable", apierrors.NewServiceUnavailable("etcdserver: request timed out")),
		Entry("TimeoutError", apierrors.NewTimeoutError("request timed out", 1)),
		Entry("TooManyRequests", apierrors.NewTooManyRequests("too many requests", 1)),
	)

	When("persistent errors occur", func() {
		JustBeforeEach(func() {
			fake.FailOnAction(&t.client.Fake, "pods", "patch", apierrors.NewServerTimeout(schema.GroupResource{}, "patch", 1), false)
		})

		Context("and the context is cancelled", func() {
			It("should return an error", func(parentCtx context.Context) {
				ctx, cancel := context.WithCancel(parentCtx)

				go func() {
					time.Sleep(350 * time.Millisecond)
					cancel()
				}()

				err := gatewayPod.SetHALabels(ctx, submarinerv1.HAStatusActive)
				Expect(err).To(HaveOccurred())
				Expect(err).To(ContainErrorSubstring(ctx.Err()))
			})
		})

		Context("and the context times out", func() {
			It("should return an error", func(parentCtx context.Context) {
				ctx, cancel := context.WithTimeout(parentCtx, 350*time.Millisecond)
				defer cancel()

				err := gatewayPod.SetHALabels(ctx, submarinerv1.HAStatusActive)
				Expect(err).To(HaveOccurred())
				Expect(err).To(ContainErrorSubstring(ctx.Err()))
			})
		})
	})

	When("errors initially occur that exhaust the first backoff cycle", func() {
		BeforeEach(func() {
			originalBackOffCap := pod.BackOffCap
			pod.BackOffCap = 500 * time.Millisecond

			DeferCleanup(func() {
				pod.BackOffCap = originalBackOffCap
			})
		})

		JustBeforeEach(func() {
			startTime := time.Now()

			t.client.Fake.PrependReactor("patch", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
				// Fail for the first 1 second, then succeed
				if time.Since(startTime) < time.Second {
					return true, nil, errors.New("mock error")
				}

				return false, nil, nil
			})
		})

		It("should retry with a fresh backoff and eventually succeed", NodeTimeout(5*time.Second), func(ctx context.Context) {
			err := gatewayPod.SetHALabels(ctx, submarinerv1.HAStatusActive)
			Expect(err).To(Succeed())
			t.verifyPodLabels(ctx, submarinerv1.HAStatusActive)
		})
	})
}

type testDriver struct {
	client *k8sfake.Clientset
}

func newTestDriver() *testDriver {
	t := &testDriver{}

	BeforeEach(func() {
		_ = os.Setenv("SUBMARINER_NAMESPACE", testNamespace)
		_ = os.Setenv("NODE_NAME", testNodeName)
		_ = os.Setenv("POD_NAME", testPodName)

		DeferCleanup(func() {
			_ = os.Unsetenv("SUBMARINER_NAMESPACE")
			_ = os.Unsetenv("NODE_NAME")
			_ = os.Unsetenv("POD_NAME")
		})

		t.client = k8sfake.NewClientset(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testPodName,
				Namespace: testNamespace,
			},
		})
	})

	return t
}

func (t *testDriver) verifyPodLabels(ctx context.Context, status submarinerv1.HAStatus) {
	updatedPod, err := t.client.CoreV1().Pods(testNamespace).Get(ctx, testPodName, metav1.GetOptions{})
	Expect(err).To(Succeed())
	Expect(updatedPod.Labels).To(HaveKeyWithValue(pod.GatewayStatusLabel, string(status)))
	Expect(updatedPod.Labels).To(HaveKeyWithValue(pod.GatewayNodeLabel, testNodeName))
}
