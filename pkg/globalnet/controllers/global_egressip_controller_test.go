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
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	fakeDynClient "github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
	"github.com/submariner-io/submariner/pkg/ipam"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("GlobalEgressIP controller", func() {
	t := newGlobalEgressIPControllerTestDriver()
	podSelector := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "foo"}}

	When("a GlobalEgressIP with no Pod selector is created", func() {
		testGlobalEgressIPCreated(t, nil)
	})

	When("a GlobalEgressIP with a Pod selector is created", func() {
		testGlobalEgressIPCreated(t, podSelector)
	})

	When("a GlobalEgressIP with allocated IPs and no Pod selector exists on startup", func() {
		testExistingGlobalEgressIP(t, nil)
	})

	When("a GlobalEgressIP with allocated IPs and a Pod selector exists on startup", func() {
		testExistingGlobalEgressIP(t, podSelector)
	})

	When("a GlobalEgressIP with no Pod selector is updated", func() {
		testGlobalEgressIPUpdated(t, nil)
	})

	When("a GlobalEgressIP with a Pod selector is updated", func() {
		testGlobalEgressIPUpdated(t, podSelector)
	})

	When("a Pod associated with a GlobalEgressIP is created", func() {
		testEgressPodEvents(t)
	})
})

func testGlobalEgressIPCreated(t *globalEgressIPControllerTestDriver, podSelector *metav1.LabelSelector) {
	var numberOfIPs *int
	var egressChain string

	BeforeEach(func() {
		numberOfIPs = nil
		if podSelector == nil {
			egressChain = constants.SmGlobalnetEgressChainForNamespace
		} else {
			egressChain = constants.SmGlobalnetEgressChainForPods
		}
	})

	JustBeforeEach(func() {
		egressIP := newGlobalEgressIP(globalEgressIPName, numberOfIPs, podSelector)
		t.createGlobalEgressIP(egressIP)
	})

	Context("with the NumberOfIPs unspecified", func() {
		It("should allocate one global IP and program the necessary IP table rules", func() {
			t.awaitGlobalEgressIPStatusAllocated(globalEgressIPName, 1)
			t.awaitIPTableRules(egressChain, getGlobalEgressIPStatus(t.globalEgressIPs, globalEgressIPName).AllocatedIPs...)
		})

		It("should start a Pod watcher", func() {
			t.watches.AwaitWatchStarted("pods")
		})
	})

	Context("with the NumberOfIPs specified", func() {
		BeforeEach(func() {
			n := 10
			numberOfIPs = &n
		})

		It("should allocate the specified number of global IPs and program the necessary IP table rules", func() {
			t.awaitGlobalEgressIPStatusAllocated(globalEgressIPName, *numberOfIPs)
			t.awaitIPTableRules(egressChain, getGlobalEgressIPStatus(t.globalEgressIPs, globalEgressIPName).AllocatedIPs...)
		})
	})

	Context("with NumberOfIPs negative", func() {
		BeforeEach(func() {
			n := -1
			numberOfIPs = &n
		})

		It("should add an appropriate Status condition", func() {
			t.awaitEgressIPStatus(t.globalEgressIPs, globalEgressIPName, 0, metav1.Condition{
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
			t.awaitEgressIPStatus(t.globalEgressIPs, globalEgressIPName, 0, metav1.Condition{
				Type:   string(submarinerv1.GlobalEgressIPAllocated),
				Status: metav1.ConditionFalse,
				Reason: "ZeroInput",
			})
		})

		It("should not start a Pod watcher", func() {
			t.watches.AwaitNoWatchStarted("pods")
		})
	})

	Context("and programming the IP table rules initially fails", func() {
		BeforeEach(func() {
			t.ipt.AddFailOnAppendRuleMatcher(Not(BeEmpty()))
			t.ipSet.AddFailOnCreateSetMatchers(Not(BeEmpty()))
		})

		It("should eventually allocate the global IP and program the IP table rules", func() {
			t.awaitEgressIPStatus(t.globalEgressIPs, globalEgressIPName, 1, metav1.Condition{
				Type:   string(submarinerv1.GlobalEgressIPAllocated),
				Status: metav1.ConditionFalse,
				Reason: "ProgramIPTableRulesFailed",
			}, metav1.Condition{
				Type:   string(submarinerv1.GlobalEgressIPAllocated),
				Status: metav1.ConditionTrue,
			})

			t.awaitIPTableRules(egressChain, getGlobalEgressIPStatus(t.globalEgressIPs, globalEgressIPName).AllocatedIPs...)
			t.watches.AwaitWatchStarted("pods")
		})
	})

	Context("with the IP pool initially exhausted", func() {
		var ips []string

		BeforeEach(func() {
			var err error

			ips, err = t.pool.Allocate(t.pool.Size())
			Expect(err).To(Succeed())
		})

		It("should eventually allocate the global IP", func() {
			t.awaitStatusConditions(t.globalEgressIPs, globalEgressIPName, metav1.Condition{
				Type:   string(submarinerv1.GlobalEgressIPAllocated),
				Status: metav1.ConditionFalse,
				Reason: "IPPoolAllocationFailed",
			})

			t.watches.AwaitNoWatchStarted("pods")
			t.dynClient.Fake.ClearActions()

			_ = t.pool.Release(ips...)

			t.awaitStatusConditions(t.globalEgressIPs, globalEgressIPName, metav1.Condition{
				Type:   string(submarinerv1.GlobalEgressIPAllocated),
				Status: metav1.ConditionTrue,
			})

			t.watches.AwaitWatchStarted("pods")
		})
	})

	Context("and then deleted", func() {
		var allocatedIPs []string
		var ipSetName string

		JustBeforeEach(func() {
			t.awaitGlobalEgressIPStatusAllocated(globalEgressIPName, 1)
			allocatedIPs = getGlobalEgressIPStatus(t.globalEgressIPs, globalEgressIPName).AllocatedIPs
			ipSetName = t.awaitIPTableRules(egressChain, allocatedIPs...)
		})

		It("should release the allocated global IPs and clean up the IP tables", func() {
			Expect(t.globalEgressIPs.Delete(context.TODO(), globalEgressIPName, metav1.DeleteOptions{})).To(Succeed())
			t.awaitIPsReleasedFromPool(allocatedIPs...)
			t.ipSet.AwaitSetDeleted(ipSetName)
			t.awaitNoIPTableRules(egressChain, allocatedIPs...)
			t.watches.AwaitWatchStopped("pods")
		})

		Context("and cleanup of the IP tables initially fails", func() {
			JustBeforeEach(func() {
				t.ipt.AddFailOnDeleteRuleMatcher(ContainSubstring(ipSetName))
				t.ipSet.AddFailOnDestroySetMatchers(Equal(ipSetName))
			})

			It("should eventually release the allocated global IPs and clean up the IP tables", func() {
				Expect(t.globalEgressIPs.Delete(context.TODO(), globalEgressIPName, metav1.DeleteOptions{})).To(Succeed())
				t.awaitIPsReleasedFromPool(allocatedIPs...)
				t.ipSet.AwaitSetDeleted(ipSetName)
				t.awaitNoIPTableRules(egressChain, allocatedIPs...)
			})
		})
	})
}

func testExistingGlobalEgressIP(t *globalEgressIPControllerTestDriver, podSelector *metav1.LabelSelector) {
	var existing *submarinerv1.GlobalEgressIP
	var egressChain string

	BeforeEach(func() {
		if podSelector == nil {
			egressChain = constants.SmGlobalnetEgressChainForNamespace
		} else {
			egressChain = constants.SmGlobalnetEgressChainForPods
		}

		n := 3
		existing = newGlobalEgressIP(globalEgressIPName, &n, podSelector)
		existing.Status.AllocatedIPs = []string{"169.254.1.100", "169.254.1.101", "169.254.1.102"}
	})

	Context("and the NumberOfIPs unchanged", func() {
		BeforeEach(func() {
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

		It("should program the necessary IP table rules for the allocated IPs", func() {
			t.awaitIPTableRules(egressChain, existing.Status.AllocatedIPs...)
		})

		It("should start a Pod watcher", func() {
			t.watches.AwaitWatchStarted("pods")
		})
	})

	Context("and the NumberOfIPs changed", func() {
		BeforeEach(func() {
			n := *existing.Spec.NumberOfIPs + 1
			existing.Spec.NumberOfIPs = &n
			t.createGlobalEgressIP(existing)
		})

		It("should reallocate the global IPs", func() {
			t.awaitGlobalEgressIPStatusAllocated(globalEgressIPName, *existing.Spec.NumberOfIPs)
			t.awaitIPTableRules(egressChain, getGlobalEgressIPStatus(t.globalEgressIPs, globalEgressIPName).AllocatedIPs...)
		})

		It("should release the previously allocated IPs", func() {
			t.awaitIPsReleasedFromPool(existing.Status.AllocatedIPs...)
			t.awaitNoIPTableRules(egressChain, existing.Status.AllocatedIPs...)
		})

		It("should start a Pod watcher", func() {
			t.watches.AwaitWatchStarted("pods")
		})
	})

	Context("and the allocated IPs are already reserved", func() {
		BeforeEach(func() {
			existing.Status.Conditions = []metav1.Condition{
				{
					Type:    string(submarinerv1.GlobalEgressIPAllocated),
					Status:  metav1.ConditionTrue,
					Reason:  "Success",
					Message: "Allocated global IPs",
				},
			}

			t.createGlobalEgressIP(existing)
			Expect(t.pool.Reserve(existing.Status.AllocatedIPs...)).To(Succeed())
		})

		It("should reallocate the global IPs", func() {
			t.awaitEgressIPStatus(t.globalEgressIPs, globalEgressIPName, *existing.Spec.NumberOfIPs, metav1.Condition{
				Type:   string(submarinerv1.GlobalEgressIPAllocated),
				Status: metav1.ConditionFalse,
				Reason: "ReserveAllocatedIPsFailed",
			}, metav1.Condition{
				Type:   string(submarinerv1.GlobalEgressIPAllocated),
				Status: metav1.ConditionTrue,
			})

			t.awaitIPTableRules(egressChain, getGlobalEgressIPStatus(t.globalEgressIPs, globalEgressIPName).AllocatedIPs...)
		})
	})

	Context("and programming the IP table rules fails", func() {
		BeforeEach(func() {
			t.createGlobalEgressIP(existing)
			t.ipt.AddFailOnAppendRuleMatcher(ContainSubstring(existing.Status.AllocatedIPs[0]))
		})

		It("should reallocate the global IPs", func() {
			t.awaitEgressIPStatus(t.globalEgressIPs, globalEgressIPName, *existing.Spec.NumberOfIPs, metav1.Condition{
				Type:   string(submarinerv1.GlobalEgressIPAllocated),
				Status: metav1.ConditionFalse,
				Reason: "ReserveAllocatedIPsFailed",
			}, metav1.Condition{
				Type:   string(submarinerv1.GlobalEgressIPAllocated),
				Status: metav1.ConditionTrue,
			})

			allocatedIPs := getGlobalEgressIPStatus(t.globalEgressIPs, globalEgressIPName).AllocatedIPs
			t.awaitIPTableRules(egressChain, allocatedIPs...)
			t.awaitIPsReleasedFromPool(existing.Status.AllocatedIPs...)
		})
	})

	Context("with previously appended Status conditions", func() {
		BeforeEach(func() {
			existing.Status.Conditions = []metav1.Condition{
				{
					Type:    string(submarinerv1.GlobalEgressIPAllocated),
					Status:  metav1.ConditionFalse,
					Reason:  "AppendedCondition1",
					Message: "Should be removed",
				},
				{
					Type:    string(submarinerv1.GlobalEgressIPAllocated),
					Status:  metav1.ConditionTrue,
					Reason:  "Success",
					Message: "Allocated global IPs",
				},
			}

			t.createGlobalEgressIP(existing)
		})

		It("should trim the Status conditions", func() {
			t.awaitStatusConditions(t.globalEgressIPs, existing.Name, metav1.Condition{
				Type:   string(submarinerv1.GlobalEgressIPAllocated),
				Status: metav1.ConditionTrue,
			})
		})
	})
}

func testGlobalEgressIPUpdated(t *globalEgressIPControllerTestDriver, podSelector *metav1.LabelSelector) {
	var (
		numberOfIPs int
		egressChain string
		existing    *submarinerv1.GlobalEgressIP
	)

	BeforeEach(func() {
		numberOfIPs = 2
		if podSelector == nil {
			egressChain = constants.SmGlobalnetEgressChainForNamespace
		} else {
			egressChain = constants.SmGlobalnetEgressChainForPods
		}

		n := numberOfIPs
		existing = newGlobalEgressIP(globalEgressIPName, &n, podSelector)
		existing.Status.AllocatedIPs = []string{"169.254.1.100", "169.254.1.101"}
		t.createGlobalEgressIP(existing)
	})

	JustBeforeEach(func() {
		t.watches.AwaitWatchStarted("pods")
		*existing.Spec.NumberOfIPs = numberOfIPs
		existing.Spec.PodSelector = podSelector
		test.UpdateResource(t.globalEgressIPs, existing)
	})

	testReallocated := func() {
		t.awaitIPsReleasedFromPool(existing.Status.AllocatedIPs...)
		t.awaitNoIPTableRules(egressChain, existing.Status.AllocatedIPs...)

		t.awaitGlobalEgressIPStatusAllocated(globalEgressIPName, *existing.Spec.NumberOfIPs)
		t.awaitIPTableRules(egressChain, getGlobalEgressIPStatus(t.globalEgressIPs, globalEgressIPName).AllocatedIPs...)

		t.watches.AwaitNoWatchStopped("pods")
	}

	Context("with the NumberOfIPs greater", func() {
		BeforeEach(func() {
			numberOfIPs++
		})

		It("should reallocate the global IPs", testReallocated)
	})

	Context("with the NumberOfIPs less", func() {
		BeforeEach(func() {
			numberOfIPs--
		})

		It("should reallocate the global IPs", testReallocated)
	})

	Context("with the NumberOfIPs zero", func() {
		BeforeEach(func() {
			numberOfIPs = 0
		})

		It("should release the previously allocated IPs and update the status", func() {
			t.awaitIPsReleasedFromPool(existing.Status.AllocatedIPs...)
			t.awaitNoIPTableRules(egressChain, existing.Status.AllocatedIPs...)

			t.awaitEgressIPStatus(t.globalEgressIPs, globalEgressIPName, 0, metav1.Condition{
				Type:   string(submarinerv1.GlobalEgressIPAllocated),
				Status: metav1.ConditionFalse,
				Reason: "ZeroInput",
			})
		})
	})

	Context("with the Pod selector changed", func() {
		BeforeEach(func() {
			if podSelector == nil {
				podSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": "foo"}}
			} else {
				podSelector = nil
			}
		})

		It("should update the status appropriately", func() {
			t.awaitEgressIPStatus(t.globalEgressIPs, globalEgressIPName, numberOfIPs, metav1.Condition{
				Type:   string(submarinerv1.GlobalEgressIPUpdated),
				Status: metav1.ConditionFalse,
				Reason: "PodSelectorUpdateNotSupported",
			})

			t.watches.AwaitNoWatchStopped("pods")
		})
	})
}

func testEgressPodEvents(t *globalEgressIPControllerTestDriver) {
	var (
		egressChain string
		pod         *corev1.Pod
		egressIP    *submarinerv1.GlobalEgressIP
		ipSet       string
	)

	BeforeEach(func() {
		egressChain = constants.SmGlobalnetEgressChainForNamespace
		pod = newPod(namespace)
		egressIP = newGlobalEgressIP(globalEgressIPName, nil, nil)
	})

	JustBeforeEach(func() {
		t.createGlobalEgressIP(egressIP)
		t.awaitGlobalEgressIPStatusAllocated(globalEgressIPName, 1)
		ipSet = t.awaitIPTableRules(egressChain, getGlobalEgressIPStatus(t.globalEgressIPs, globalEgressIPName).AllocatedIPs...)
		t.createPod(pod)
	})

	Context("in the same namespace as the GlobalEgressIP", func() {
		Context("and the GlobalEgressIP has no Pod selector", func() {
			It("should add the Pod IP to the IP set", func() {
				t.ipSet.AwaitEntry(ipSet, pod.Status.PodIP)
			})
		})

		Context("", func() {
			BeforeEach(func() {
				egressChain = constants.SmGlobalnetEgressChainForPods
				egressIP.Spec.PodSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": "foo"}}
			})

			Context("and it matches the Pod selector", func() {
				BeforeEach(func() {
					pod.Labels = egressIP.Spec.PodSelector.MatchLabels
				})

				It("should add the Pod IP to the IP set", func() {
					t.ipSet.AwaitEntry(ipSet, pod.Status.PodIP)
				})
			})

			Context("and it does not match the Pod selector", func() {
				It("should not add the Pod IP to the IP set", func() {
					t.ipSet.AwaitNoEntry(ipSet, pod.Status.PodIP)
				})
			})
		})

		Context("and then deleted", func() {
			JustBeforeEach(func() {
				t.ipSet.AwaitEntry(ipSet, pod.Status.PodIP)
				t.deletePod(pod)
			})

			It("should remove the Pod IP from the IP set", func() {
				t.ipSet.AwaitEntryDeleted(ipSet, pod.Status.PodIP)
			})

			Context("and removal from the IP set initially fails", func() {
				BeforeEach(func() {
					t.ipSet.AddFailOnDelEntryMatchers(pod.Status.PodIP)
				})

				It("should eventually remove the Pod IP from the IP set", func() {
					t.ipSet.AwaitEntryDeleted(ipSet, pod.Status.PodIP)
				})
			})
		})

		Context("and it doesn't initially have a Pod IP", func() {
			BeforeEach(func() {
				pod.Status.PodIP = ""
			})

			JustBeforeEach(func() {
				t.ipSet.AwaitNoEntry(ipSet, "")
				pod.Status.PodIP = "1.2.3.4"
				test.UpdateResource(t.pods.Namespace(pod.Namespace), pod)
			})

			It("should eventually add the Pod IP to the IP set", func() {
				t.ipSet.AwaitEntry(ipSet, pod.Status.PodIP)
			})
		})

		Context("and addition to the IP set initially fails", func() {
			BeforeEach(func() {
				t.ipSet.AddFailOnAddEntryMatchers(pod.Status.PodIP)
			})

			It("should eventually add the Pod IP to the IP set", func() {
				t.ipSet.AwaitEntry(ipSet, pod.Status.PodIP)
			})
		})
	})

	Context("in a different namespace as the GlobalEgressIP", func() {
		BeforeEach(func() {
			pod.Namespace = "foo"
		})

		It("should not add the Pod IP to the IP set", func() {
			t.ipSet.AwaitNoEntry(ipSet, pod.Status.PodIP)
		})
	})
}

type globalEgressIPControllerTestDriver struct {
	*testDriverBase
}

func newGlobalEgressIPControllerTestDriver() *globalEgressIPControllerTestDriver {
	t := &globalEgressIPControllerTestDriver{}

	BeforeEach(func() {
		t.testDriverBase = newTestDriverBase()

		var err error

		t.pool, err = ipam.NewIPPool(t.globalCIDR)
		Expect(err).To(Succeed())

		t.watches = fakeDynClient.NewWatchReactor(&t.dynClient.Fake)
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

	t.controller, err = controllers.NewGlobalEgressIPController(&syncer.ResourceSyncerConfig{
		SourceClient: t.dynClient,
		RestMapper:   t.restMapper,
		Scheme:       t.scheme,
	}, t.pool)

	Expect(err).To(Succeed())
	Expect(t.controller.Start()).To(Succeed())
}

func (t *globalEgressIPControllerTestDriver) awaitIPTableRules(chain string, ips ...string) string {
	set := t.ipSet.AwaitOneSet(HavePrefix(controllers.IPSetPrefix))
	t.ipt.AwaitRule("nat", chain, And(ContainSubstring(set), ContainSubstring(getSNATAddress(ips...))))

	return set
}

func (t *globalEgressIPControllerTestDriver) awaitNoIPTableRules(chain string, ips ...string) {
	t.ipt.AwaitNoRule("nat", chain, ContainSubstring(getSNATAddress(ips...)))
}
