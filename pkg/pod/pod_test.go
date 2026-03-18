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
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/fake"
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
		It("should create GatewayPod successfully and set passive HA status", func() {
			gw, err := pod.NewGatewayPod(context.TODO(), t.client)

			Expect(err).NotTo(HaveOccurred())
			Expect(gw).ToNot(BeNil())
			t.verifyPodLabels(submarinerv1.HAStatusPassive)
		})
	})

	When("SUBMARINER_NAMESPACE is not set", func() {
		BeforeEach(func() {
			os.Unsetenv("SUBMARINER_NAMESPACE")
		})

		It("should return an error", func() {
			_, err := pod.NewGatewayPod(context.TODO(), t.client)
			Expect(err).To(HaveOccurred())
		})
	})

	When("NODE_NAME is not set", func() {
		BeforeEach(func() {
			os.Unsetenv("NODE_NAME")
		})

		It("should return an error", func() {
			_, err := pod.NewGatewayPod(context.TODO(), t.client)
			Expect(err).To(HaveOccurred())
		})
	})

	When("POD_NAME is not set", func() {
		BeforeEach(func() {
			os.Unsetenv("POD_NAME")
		})

		It("should return an error", func() {
			_, err := pod.NewGatewayPod(context.TODO(), t.client)
			Expect(err).To(HaveOccurred())
		})
	})

	When("setting initial passive HA status fails", func() {
		BeforeEach(func() {
			fake.FailOnAction(&t.client.Fake, "pods", "patch", apierrors.NewNotFound(schema.GroupResource{}, testPodName), false)
		})

		It("should return an error", func() {
			_, err := pod.NewGatewayPod(context.TODO(), t.client)
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
		It("should update the pod labels successfully", func() {
			err := gatewayPod.SetHALabels(context.TODO(), submarinerv1.HAStatusActive)
			Expect(err).To(Succeed())
			t.verifyPodLabels(submarinerv1.HAStatusActive)
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

		It("should retry and eventually succeed", func() {
			err := gatewayPod.SetHALabels(context.TODO(), submarinerv1.HAStatusActive)
			Expect(err).To(Succeed())
			t.verifyPodLabels(submarinerv1.HAStatusActive)
		})
	})

	When("the patch operation initially returns a transient error", func() {
		JustBeforeEach(func() {
			fake.FailOnAction(&t.client.Fake, "pods", "patch", apierrors.NewServerTimeout(schema.GroupResource{}, "patch", 1), true)
		})

		It("should retry and eventually succeed", func() {
			err := gatewayPod.SetHALabels(context.TODO(), submarinerv1.HAStatusActive)
			Expect(err).To(Succeed())
			t.verifyPodLabels(submarinerv1.HAStatusActive)
		})
	})

	When("the patch operation returns a non-transient error", func() {
		JustBeforeEach(func() {
			fake.FailOnAction(&t.client.Fake, "pods", "patch", apierrors.NewNotFound(schema.GroupResource{}, testPodName), false)
		})

		It("should return an error without retrying", func() {
			err := gatewayPod.SetHALabels(context.TODO(), submarinerv1.HAStatusActive)
			Expect(err).To(HaveOccurred())
		})
	})

	When("multiple transient errors occur", func() {
		JustBeforeEach(func() {
			fake.FailOnAction(&t.client.Fake, "pods", "patch", apierrors.NewServerTimeout(schema.GroupResource{}, "patch", 1), false)
		})

		Context("and the context is cancelled", func() {
			It("should return an error", func() {
				ctx, cancel := context.WithCancel(context.Background())

				go func() {
					time.Sleep(350 * time.Millisecond)
					cancel()
				}()

				err := gatewayPod.SetHALabels(ctx, submarinerv1.HAStatusActive)
				Expect(err).To(HaveOccurred())
				Expect(err).To(Equal(ctx.Err()))
			})
		})

		Context("and the context times out", func() {
			It("should return an error", func() {
				ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
				defer cancel()

				err := gatewayPod.SetHALabels(ctx, submarinerv1.HAStatusActive)
				Expect(err).To(HaveOccurred())
				Expect(err).To(Equal(ctx.Err()))
			})
		})
	})

	When("multiple transient errors occur with different error messages", func() {
		JustBeforeEach(func() {
			attemptCount := 0
			errorMessages := []string{
				"first transient error",
				"first transient error", // Repeat to test duplicate consecutive message suppression
				"second transient error",
				"first transient error",
			}

			t.client.Fake.PrependReactor("patch", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
				if attemptCount < len(errorMessages) {
					err := apierrors.NewTooManyRequests(errorMessages[attemptCount], 1)
					attemptCount++

					return true, nil, err
				}

				return false, nil, nil
			})
		})

		It("should log each unique error message only once", func() {
			// For now, log output is visually inspected. A future enhancement would be to implement a log sink that captures output.
			err := gatewayPod.SetHALabels(context.TODO(), submarinerv1.HAStatusActive)
			Expect(err).To(Succeed())
			t.verifyPodLabels(submarinerv1.HAStatusActive)
		})
	})
}

type testDriver struct {
	client *k8sfake.Clientset
}

func newTestDriver() *testDriver {
	t := &testDriver{}

	BeforeEach(func() {
		os.Setenv("SUBMARINER_NAMESPACE", testNamespace)
		os.Setenv("NODE_NAME", testNodeName)
		os.Setenv("POD_NAME", testPodName)

		t.client = k8sfake.NewClientset(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testPodName,
				Namespace: testNamespace,
			},
		})
	})

	AfterEach(func() {
		os.Unsetenv("SUBMARINER_NAMESPACE")
		os.Unsetenv("NODE_NAME")
		os.Unsetenv("POD_NAME")
	})

	return t
}

func (t *testDriver) verifyPodLabels(status submarinerv1.HAStatus) {
	updatedPod, err := t.client.CoreV1().Pods(testNamespace).Get(context.TODO(), testPodName, metav1.GetOptions{})
	Expect(err).To(Succeed())
	Expect(updatedPod.Labels).To(HaveKeyWithValue(pod.GatewayStatusLabel, string(status)))
	Expect(updatedPod.Labels).To(HaveKeyWithValue(pod.GatewayNodeLabel, testNodeName))
}
