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

// import (
// 	. "github.com/onsi/ginkgo"
// 	. "github.com/onsi/gomega"
// )

// var _ = Describe("GlobalEgressIP controller", func() {
// 	t := newGlobalEgressIPControllerTestDriver()
// 	podSelector := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "foo"}}

// 	When("a GlobalEgressIP with no Pod selector is created", func() {
// 		testGlobalEgressIPCreated(t, nil)
// 	})

// 	When("a GlobalEgressIP with a Pod selector is created", func() {
// 		testGlobalEgressIPCreated(t, podSelector)
// 	})

// 	When("a GlobalEgressIP with allocated IPs and no Pod selector exists on startup", func() {
// 		testExistingGlobalEgressIP(t, nil)
// 	})

// 	When("a GlobalEgressIP with allocated IPs and a Pod selector exists on startup", func() {
// 		testExistingGlobalEgressIP(t, podSelector)
// 	})

// 	When("a GlobalEgressIP with no Pod selector is updated", func() {
// 		testGlobalEgressIPUpdated(t, nil)
// 	})

// 	When("a GlobalEgressIP with a Pod selector is updated", func() {
// 		testGlobalEgressIPUpdated(t, podSelector)
// 	})

// })

// func testGlobalEgressIPCreated(t *globalEgressIPControllerTestDriver, podSelector *metav1.LabelSelector) {
// 	var numberOfIPs *int

// 	BeforeEach(func() {
// 		numberOfIPs = nil
// 	})

// 	JustBeforeEach(func() {
// 		egressIP := newGlobalEgressIP(globalEgressIPName, numberOfIPs, podSelector)
// 		t.createGlobalEgressIP(egressIP)
// 	})

// 	Context("with the NumberOfIPs unspecified", func() {
// 		It("should allocate one global IP", func() {
// 			t.awaitGlobalEgressIPStatusAllocated(globalEgressIPName, 1)
// 		})

// 		It("should start a Pod watcher", func() {
// 			t.watches.AwaitWatchStarted("pods")
// 		})
// 	})

// 	Context("with the NumberOfIPs specified", func() {
// 		BeforeEach(func() {
// 			n := 10
// 			numberOfIPs = &n
// 		})

// 		It("should allocate the specified number of global IPs", func() {
// 			t.awaitGlobalEgressIPStatusAllocated(globalEgressIPName, *numberOfIPs)
// 		})
// 	})

// 	Context("with NumberOfIPs negative", func() {
// 		BeforeEach(func() {
// 			n := -1
// 			numberOfIPs = &n
// 		})

// 		It("should add an appropriate Status condition", func() {
// 			t.awaitEgressIPStatus(t.globalEgressIPs, globalEgressIPName, 0, 0, metav1.Condition{
// 				Type:   string(submarinerv1.GlobalEgressIPAllocated),
// 				Status: metav1.ConditionFalse,
// 				Reason: "InvalidInput",
// 			})
// 		})
// 	})

// 	Context("with NumberOfIPs zero", func() {
// 		BeforeEach(func() {
// 			n := 0
// 			numberOfIPs = &n
// 		})

// 		It("should add an appropriate Status condition", func() {
// 			t.awaitEgressIPStatus(t.globalEgressIPs, globalEgressIPName, 0, 0, metav1.Condition{
// 				Type:   string(submarinerv1.GlobalEgressIPAllocated),
// 				Status: metav1.ConditionFalse,
// 				Reason: "ZeroInput",
// 			})
// 		})

// 		It("should not start a Pod watcher", func() {
// 			t.watches.AwaitNoWatchStarted("pods")
// 		})
// 	})

// 	Context("with the IP pool initially exhausted", func() {
// 		var ips []string

// 		BeforeEach(func() {
// 			var err error

// 			ips, err = t.pool.Allocate(t.pool.Size())
// 			Expect(err).To(Succeed())
// 		})

// 		It("should eventually allocate the global IP", func() {
// 			awaitStatusConditions(t.globalEgressIPs, globalEgressIPName, 0, metav1.Condition{
// 				Type:   string(submarinerv1.GlobalEgressIPAllocated),
// 				Status: metav1.ConditionFalse,
// 				Reason: "IPPoolAllocationFailed",
// 			})

// 			t.watches.AwaitNoWatchStarted("pods")

// 			_ = t.pool.Release(ips...)

// 			awaitStatusConditions(t.globalEgressIPs, globalEgressIPName, 1, metav1.Condition{
// 				Type:   string(submarinerv1.GlobalEgressIPAllocated),
// 				Status: metav1.ConditionFalse,
// 			})

// 			t.watches.AwaitWatchStarted("pods")
// 		})
// 	})

// 	Context("and then deleted", func() {
// 		var allocatedIPs []string

// 		JustBeforeEach(func() {
// 			t.awaitGlobalEgressIPStatusAllocated(globalEgressIPName, 1)
// 			allocatedIPs = getGlobalEgressIPStatus(t.globalEgressIPs, globalEgressIPName).AllocatedIPs
// 		})

// 		It("should release the allocated global IPs", func() {
// 			Expect(t.globalEgressIPs.Delete(context.TODO(), globalEgressIPName, metav1.DeleteOptions{})).To(Succeed())
// 			t.awaitIPsReleasedFromPool(allocatedIPs...)
// 			t.watches.AwaitWatchStopped("pods")
// 		})
// 	})
// }

// func testExistingGlobalEgressIP(t *globalEgressIPControllerTestDriver, podSelector *metav1.LabelSelector) {
// 	var existing *submarinerv1.GlobalEgressIP

// 	BeforeEach(func() {

// 		n := 3
// 		existing = newGlobalEgressIP(globalEgressIPName, &n, podSelector)
// 		existing.Status.AllocatedIPs = []string{"169.254.1.100", "169.254.1.101", "169.254.1.102"}
// 	})

// 	Context("and the NumberOfIPs unchanged", func() {
// 		BeforeEach(func() {
// 			t.createGlobalEgressIP(existing)
// 		})

// 		It("should not reallocate the global IPs", func() {
// 			Consistently(func() []string {
// 				return getGlobalEgressIPStatus(t.globalEgressIPs, existing.Name).AllocatedIPs
// 			}, 200*time.Millisecond).Should(Equal(existing.Status.AllocatedIPs))
// 		})

// 		It("should not update the Status conditions", func() {
// 			Consistently(func() int {
// 				return len(getGlobalEgressIPStatus(t.globalEgressIPs, existing.Name).Conditions)
// 			}, 200*time.Millisecond).Should(Equal(0))
// 		})

// 		It("should reserve the previously allocated IPs", func() {
// 			t.verifyIPsReservedInPool(getGlobalEgressIPStatus(t.globalEgressIPs, existing.Name).AllocatedIPs...)
// 		})

// 		It("should start a Pod watcher", func() {
// 			t.watches.AwaitWatchStarted("pods")
// 		})
// 	})

// 	Context("and the NumberOfIPs changed", func() {
// 		BeforeEach(func() {
// 			n := *existing.Spec.NumberOfIPs + 1
// 			existing.Spec.NumberOfIPs = &n
// 			t.createGlobalEgressIP(existing)
// 		})

// 		It("should reallocate the global IPs", func() {
// 			t.awaitGlobalEgressIPStatusAllocated(globalEgressIPName, *existing.Spec.NumberOfIPs)
// 		})

// 		It("should start a Pod watcher", func() {
// 			t.watches.AwaitWatchStarted("pods")
// 		})
// 	})

// 	Context("and the allocated IPs are already reserved", func() {
// 		BeforeEach(func() {
// 			existing.Status.Conditions = []metav1.Condition{
// 				{
// 					Type:    string(submarinerv1.GlobalEgressIPAllocated),
// 					Status:  metav1.ConditionTrue,
// 					Reason:  "Success",
// 					Message: "Allocated global IPs",
// 				},
// 			}

// 			t.createGlobalEgressIP(existing)
// 			Expect(t.pool.Reserve(existing.Status.AllocatedIPs...)).To(Succeed())
// 		})

// 		It("should reallocate the global IPs", func() {
// 			t.awaitEgressIPStatus(t.globalEgressIPs, globalEgressIPName, *existing.Spec.NumberOfIPs, 1,
// 				metav1.Condition{
// 					Type:   string(submarinerv1.GlobalEgressIPAllocated),
// 					Status: metav1.ConditionFalse,
// 					Reason: "ReserveAllocatedIPsFailed",
// 				}, metav1.Condition{
// 					Type:   string(submarinerv1.GlobalEgressIPAllocated),
// 					Status: metav1.ConditionTrue,
// 				})
// 		})
// 	})
// }

// func testGlobalEgressIPUpdated(t *globalEgressIPControllerTestDriver, podSelector *metav1.LabelSelector) {
// 	var (
// 		numberOfIPs int
// 		existing    *submarinerv1.GlobalEgressIP
// 	)

// 	BeforeEach(func() {
// 		numberOfIPs = 2

// 		n := numberOfIPs
// 		existing = newGlobalEgressIP(globalEgressIPName, &n, podSelector)
// 		existing.Status.AllocatedIPs = []string{"169.254.1.100", "169.254.1.101"}
// 		t.createGlobalEgressIP(existing)
// 	})

// 	JustBeforeEach(func() {
// 		t.watches.AwaitWatchStarted("pods")
// 		*existing.Spec.NumberOfIPs = numberOfIPs
// 		existing.Spec.PodSelector = podSelector
// 		test.UpdateResource(t.globalEgressIPs, existing)
// 	})

// 	testReallocated := func() {
// 		t.awaitIPsReleasedFromPool(existing.Status.AllocatedIPs...)

// 		t.awaitGlobalEgressIPStatusAllocated(globalEgressIPName, *existing.Spec.NumberOfIPs)

// 		t.watches.AwaitNoWatchStopped("pods")
// 	}

// 	Context("with the NumberOfIPs greater", func() {
// 		BeforeEach(func() {
// 			numberOfIPs++
// 		})

// 		It("should reallocate the global IPs", testReallocated)
// 	})

// 	Context("with the NumberOfIPs less", func() {
// 		BeforeEach(func() {
// 			numberOfIPs--
// 		})

// 		It("should reallocate the global IPs", testReallocated)
// 	})

// 	Context("with the NumberOfIPs zero", func() {
// 		BeforeEach(func() {
// 			numberOfIPs = 0
// 		})

// 		It("should release the previously allocated IPs and update the status", func() {
// 			t.awaitIPsReleasedFromPool(existing.Status.AllocatedIPs...)

// 			t.awaitEgressIPStatus(t.globalEgressIPs, globalEgressIPName, 0, 0, metav1.Condition{
// 				Type:   string(submarinerv1.GlobalEgressIPAllocated),
// 				Status: metav1.ConditionFalse,
// 				Reason: "ZeroInput",
// 			})
// 		})
// 	})

// 	Context("with the Pod selector changed", func() {
// 		BeforeEach(func() {
// 			if podSelector == nil {
// 				podSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": "foo"}}
// 			} else {
// 				podSelector = nil
// 			}
// 		})

// 		It("should update the status appropriately", func() {
// 			t.awaitEgressIPStatus(t.globalEgressIPs, globalEgressIPName, numberOfIPs, 0, metav1.Condition{
// 				Type:   string(submarinerv1.GlobalEgressIPUpdated),
// 				Status: metav1.ConditionFalse,
// 				Reason: "PodSelectorUpdateNotSupported",
// 			})

// 			t.watches.AwaitNoWatchStopped("pods")
// 		})
// 	})
// }

// type globalEgressIPControllerTestDriver struct {
// 	*testDriverBase
// }

// func newGlobalEgressIPControllerTestDriver() *globalEgressIPControllerTestDriver {
// 	t := &globalEgressIPControllerTestDriver{}

// 	BeforeEach(func() {
// 		t.testDriverBase = newTestDriverBase()

// 		var err error

// 		t.pool, err = ipam.NewIPPool(t.globalCIDR)
// 		Expect(err).To(Succeed())
// 	})

// 	JustBeforeEach(func() {
// 		t.start()
// 	})

// 	AfterEach(func() {
// 		t.testDriverBase.afterEach()
// 	})

// 	return t
// }

// func (t *globalEgressIPControllerTestDriver) start() {
// 	var err error

// 	t.controller, err = controllers.NewGlobalEgressIPController(&syncer.ResourceSyncerConfig{
// 		SourceClient: t.dynClient,
// 		RestMapper:   t.restMapper,
// 		Scheme:       t.scheme,
// 	}, t.pool)

// 	Expect(err).To(Succeed())
// 	Expect(t.controller.Start()).To(Succeed())
// }
