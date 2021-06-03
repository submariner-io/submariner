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
package controllers_test

import (
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
	"github.com/submariner-io/submariner/pkg/ipam"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("GlobalEgressIP controller", func() {
	t := newGlobalEgressIPControllerTestDriver()

	When("a GlobalEgressIP is created", func() {
		var numberOfIPs *int

		JustBeforeEach(func() {
			t.createGlobalEgressIP(newGlobalEgressIP(globalEgressIPName, numberOfIPs, nil))
		})

		Context("with the NumberOfIPs unspecified", func() {
			It("should successfully allocate one global IP", func() {
				t.awaitGlobalEgressIPStatusAllocated(globalEgressIPName, 1)
				// TODO - verify IP tables
			})
		})

		Context("with the NumberOfIPs specified", func() {
			BeforeEach(func() {
				n := 10
				numberOfIPs = &n
			})

			It("should successfully allocate the specified number of global IPs", func() {
				t.awaitGlobalEgressIPStatusAllocated(globalEgressIPName, *numberOfIPs)
				// TODO - verify IP tables
			})
		})

		Context("with NumberOfIPs negative", func() {
			BeforeEach(func() {
				n := -1
				numberOfIPs = &n
			})

			It("should add an appropriate Status condition", func() {
				t.awaitGlobalEgressIPStatus(t.globalEgressIPs, globalEgressIPName, t.globalCIDR,
					0, 0, metav1.Condition{
						Type:   string(submarinerv1.GlobalEgressIPAllocated),
						Status: metav1.ConditionFalse,
						Reason: "InvalidInput",
					})
			})
		})

		Context("with NumberOfIPs zero", func() {
			BeforeEach(func() {
				n := 0
				numberOfIPs = &n
			})

			It("should add an appropriate Status condition", func() {
				t.awaitGlobalEgressIPStatus(t.globalEgressIPs, globalEgressIPName, t.globalCIDR,
					0, 0, metav1.Condition{
						Type:   string(submarinerv1.GlobalEgressIPAllocated),
						Status: metav1.ConditionFalse,
						Reason: "ZeroInput",
					})
			})
		})
	})

	When("a GlobalEgressIP exists on startup", func() {
		var existing *submarinerv1.GlobalEgressIP

		BeforeEach(func() {
			n := 3
			existing = newGlobalEgressIP(globalEgressIPName, &n, nil)
			existing.Status.AllocatedIPs = []string{"169.254.1.100", "169.254.1.101", "169.254.1.102"}
			t.createGlobalEgressIP(existing)
		})

		It("should not reallocate the global IPs", func() {
			Consistently(func() []string {
				return getGlobalEgressIPStatus(t.globalEgressIPs, existing.Name).AllocatedIPs
			}, 200*time.Millisecond).Should(Equal(existing.Status.AllocatedIPs))
		})

		It("should not update the Status conditions", func() {
			Consistently(func() int {
				return len(getGlobalEgressIPStatus(t.globalEgressIPs, existing.Name).Conditions)
			}, 200*time.Millisecond).Should(Equal(0))
		})

		It("should reserve the previously allocated IPs", func() {
			t.verifyIPsReservedInPool(getGlobalEgressIPStatus(t.globalEgressIPs, existing.Name).AllocatedIPs...)
		})
	})

	When("a Pod is created", func() {
		var pod *corev1.Pod

		JustBeforeEach(func() {
			pod = newPod(namespace)
		})

		JustBeforeEach(func() {
			t.createGlobalEgressIP(newGlobalEgressIP(globalEgressIPName, nil, nil))
			t.awaitGlobalEgressIPStatusAllocated(globalEgressIPName, 1)

			t.createPod(pod)
		})

		Context("in the same namespace as a GlobalEgressIP", func() {
			It("should add an appropriate Pod IP tables rule", func() {
				// TODO verify add
			})

			Context("and then deleted", func() {
				JustBeforeEach(func() {
					// TODO verify add

					t.deletePod(pod)
				})

				It("should remove the Pod IP tables rule", func() {
					// TODO verify remove
				})
			})
		})

		Context("in a different namespace as a GlobalEgressIP", func() {
			It("should not add an IP tables rule", func() {
				// TODO verify
			})
		})
	})
})

type globalEgressIPControllerTestDriver struct {
	*testDriverBase
}

func newGlobalEgressIPControllerTestDriver() *globalEgressIPControllerTestDriver {
	t := &globalEgressIPControllerTestDriver{}

	BeforeEach(func() {
		t.testDriverBase = newTestDriverBase()
	})

	JustBeforeEach(func() {
		t.start()
	})

	AfterEach(func() {
		t.testDriverBase.afterEach()
	})

	return t
}

func (t *globalEgressIPControllerTestDriver) start() {
	var err error

	t.pool, err = ipam.NewIPPool(t.globalCIDR)
	Expect(err).To(Succeed())

	t.controller, err = controllers.NewGlobalEgressIPController(syncer.ResourceSyncerConfig{
		SourceClient: t.dynClient,
		RestMapper:   t.restMapper,
		Scheme:       t.scheme,
	}, t.pool)

	Expect(err).To(Succeed())
	Expect(t.controller.Start()).To(Succeed())
}
