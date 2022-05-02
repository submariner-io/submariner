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

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
	"github.com/submariner-io/submariner/pkg/ipam"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("GlobalIngressIP controller", func() {
	t := newGlobalIngressIPControllerDriver()

	podIP := "10.1.2.3"

	clusterIPServiceIngress := &submarinerv1.GlobalIngressIP{
		ObjectMeta: metav1.ObjectMeta{
			Name: globalIngressIPName,
		},
		Spec: submarinerv1.GlobalIngressIPSpec{
			Target: submarinerv1.ClusterIPService,
			ServiceRef: &corev1.LocalObjectReference{
				Name: "nginx",
			},
		},
	}

	headlessServiceIngress := &submarinerv1.GlobalIngressIP{
		ObjectMeta: metav1.ObjectMeta{
			Name: globalIngressIPName,
			Annotations: map[string]string{
				"submariner.io/headless-svc-pod-ip": podIP,
			},
		},
		Spec: submarinerv1.GlobalIngressIPSpec{
			Target: submarinerv1.HeadlessServicePod,
			ServiceRef: &corev1.LocalObjectReference{
				Name: "db-service",
			},
			PodRef: &corev1.LocalObjectReference{
				Name: "pod-1",
			},
		},
	}

	awaitHeadlessServicePodRules := func(ip string) {
		t.awaitPodEgressRules(podIP, ip)
		t.awaitPodIngressRules(podIP, ip)
	}

	awaitNoHeadlessServicePodRules := func(ip string) {
		t.awaitNoPodEgressRules(podIP, ip)
		t.awaitNoPodIngressRules(podIP, ip)
	}

	endpointsIP := "10.4.5.6"

	headlessServiceWithoutSelectorIngress := &submarinerv1.GlobalIngressIP{
		ObjectMeta: metav1.ObjectMeta{
			Name: globalIngressIPName,
			Annotations: map[string]string{
				"submariner.io/headless-svc-endpoints-ip": endpointsIP,
			},
		},
		Spec: submarinerv1.GlobalIngressIPSpec{
			Target: submarinerv1.HeadlessServiceEndpoints,
			ServiceRef: &corev1.LocalObjectReference{
				Name: "db-service",
			},
		},
	}

	awaitHeadlessServiceEndpointsRules := func(ip string) {
		t.awaitEndpointsEgressRules(endpointsIP, ip)
		t.awaitEndpointsIngressRules(endpointsIP, ip)
	}

	awaitNoHeadlessServiceEndpointsRules := func(ip string) {
		t.awaitNoEndpointsEgressRules(endpointsIP, ip)
		t.awaitNoEndpointsIngressRules(endpointsIP, ip)
	}

	When("a GlobalIngressIP for a cluster IP Service is created", func() {
		testGlobalIngressIPCreatedClusterIPSvc(t, clusterIPServiceIngress)
	})

	When("a GlobalIngressIP for a headless Service is created", func() {
		testGlobalIngressIPCreatedHeadlessSvc(t, headlessServiceIngress, awaitHeadlessServicePodRules, awaitNoHeadlessServicePodRules, podIP)
	})

	When("a GlobalIngressIP for a cluster IP Service exists on startup", func() {
		testExistingGlobalIngressIPClusterIPSvc(t, clusterIPServiceIngress)
	})

	When("a GlobalIngressIP for a headless Service exists on startup", func() {
		testExistingGlobalIngressIPHeadlessSvc(t, headlessServiceIngress, awaitHeadlessServicePodRules)
	})

	When("a GlobalIngressIP for a headless Service without selector is created", func() {
		testGlobalIngressIPCreatedHeadlessSvc(t, headlessServiceWithoutSelectorIngress, awaitHeadlessServiceEndpointsRules,
			awaitNoHeadlessServiceEndpointsRules, endpointsIP)
	})
})

func testGlobalIngressIPCreatedClusterIPSvc(t *globalIngressIPControllerTestDriver, ingressIP *submarinerv1.GlobalIngressIP) {
	JustBeforeEach(func() {
		service := newClusterIPService()
		t.createService(service)
		t.createGlobalIngressIP(ingressIP)
	})

	It("should successfully allocate a global IP", func() {
		t.awaitIngressIPStatusAllocated(globalIngressIPName)
		allocatedIP := t.getGlobalIngressIPStatus(globalIngressIPName).AllocatedIP
		Expect(allocatedIP).ToNot(BeEmpty())
	})

	It("should successfully create an internal submariner service", func() {
		internalSvcName := controllers.GetInternalSvcName(serviceName)
		intSvc := t.awaitService(internalSvcName)
		externalIP := intSvc.Spec.ExternalIPs[0]
		Expect(externalIP).ToNot(BeEmpty())

		finalizer := intSvc.GetFinalizers()[0]
		Expect(finalizer).To(Equal(controllers.InternalServiceFinalizer))
	})

	Context("with the IP pool exhausted", func() {
		BeforeEach(func() {
			_, err := t.pool.Allocate(t.pool.Size())
			Expect(err).To(Succeed())
		})

		It("should add an appropriate Status condition", func() {
			awaitStatusConditions(t.globalIngressIPs, globalIngressIPName, 0, metav1.Condition{
				Type:   string(submarinerv1.GlobalEgressIPAllocated),
				Status: metav1.ConditionFalse,
				Reason: "IPPoolAllocationFailed",
			})
		})
	})

	Context("and then removed", func() {
		var allocatedIP string

		JustBeforeEach(func() {
			t.awaitIngressIPStatusAllocated(globalIngressIPName)
			allocatedIP = t.getGlobalIngressIPStatus(globalIngressIPName).AllocatedIP

			Expect(t.globalIngressIPs.Delete(context.TODO(), globalIngressIPName, metav1.DeleteOptions{})).To(Succeed())
		})

		It("should release the allocated global IP", func() {
			t.awaitIPsReleasedFromPool(allocatedIP)
		})

		It("should delete the internal service", func() {
			internalSvcName := controllers.GetInternalSvcName(serviceName)
			t.awaitNoService(internalSvcName)
		})
	})
}

func testGlobalIngressIPCreatedHeadlessSvc(t *globalIngressIPControllerTestDriver, ingressIP *submarinerv1.GlobalIngressIP,
	awaitIPTableRules, awaitNoIPTableRules func(string), ruleMatch string,
) {
	JustBeforeEach(func() {
		service := newClusterIPService()
		t.createService(service)
		t.createGlobalIngressIP(ingressIP)
	})

	It("should successfully allocate a global IP", func() {
		t.awaitIngressIPStatusAllocated(globalIngressIPName)
		allocatedIP := t.getGlobalIngressIPStatus(globalIngressIPName).AllocatedIP
		Expect(allocatedIP).ToNot(BeEmpty())
		awaitIPTableRules(allocatedIP)
	})

	Context("with the IP pool exhausted", func() {
		BeforeEach(func() {
			_, err := t.pool.Allocate(t.pool.Size())
			Expect(err).To(Succeed())
		})

		It("should add an appropriate Status condition", func() {
			awaitStatusConditions(t.globalIngressIPs, globalIngressIPName, 0, metav1.Condition{
				Type:   string(submarinerv1.GlobalEgressIPAllocated),
				Status: metav1.ConditionFalse,
				Reason: "IPPoolAllocationFailed",
			})
		})
	})

	Context("and programming of IP tables initially fails", func() {
		BeforeEach(func() {
			t.ipt.AddFailOnAppendRuleMatcher(ContainSubstring(ruleMatch))
		})

		It("should eventually allocate a global IP", func() {
			awaitStatusConditions(t.globalIngressIPs, globalIngressIPName, 0, metav1.Condition{
				Type:   string(submarinerv1.GlobalEgressIPAllocated),
				Status: metav1.ConditionFalse,
				Reason: "ProgramIPTableRulesFailed",
			}, metav1.Condition{
				Type:   string(submarinerv1.GlobalEgressIPAllocated),
				Status: metav1.ConditionTrue,
			})

			awaitIPTableRules(t.getGlobalIngressIPStatus(globalIngressIPName).AllocatedIP)
		})
	})

	Context("and then removed", func() {
		var allocatedIP string

		JustBeforeEach(func() {
			t.awaitIngressIPStatusAllocated(globalIngressIPName)
			allocatedIP = t.getGlobalIngressIPStatus(globalIngressIPName).AllocatedIP

			Expect(t.globalIngressIPs.Delete(context.TODO(), globalIngressIPName, metav1.DeleteOptions{})).To(Succeed())
		})

		It("should release the allocated global IP", func() {
			t.awaitIPsReleasedFromPool(allocatedIP)
			awaitNoIPTableRules(allocatedIP)
		})

		Context("and cleanup of IP tables initially fails", func() {
			BeforeEach(func() {
				t.ipt.AddFailOnDeleteRuleMatcher(ContainSubstring(ruleMatch))
			})

			It("should eventually cleanup the IP tables and reallocate", func() {
				t.awaitIPsReleasedFromPool(allocatedIP)
				awaitNoIPTableRules(allocatedIP)
			})
		})
	})
}

func testExistingGlobalIngressIPClusterIPSvc(t *globalIngressIPControllerTestDriver, ingressIP *submarinerv1.GlobalIngressIP) {
	var existing *submarinerv1.GlobalIngressIP

	BeforeEach(func() {
		existing = ingressIP.DeepCopy()
	})

	Context("with an allocated IP", func() {
		BeforeEach(func() {
			existing.Status.AllocatedIP = globalIP
			exportedService := newClusterIPService()
			t.createService(exportedService)

			internalSvc := newGlobalnetInternalService(controllers.GetInternalSvcName(serviceName))
			internalSvc.Spec.ExternalIPs = []string{existing.Status.AllocatedIP}
			t.createService(internalSvc)
			t.createGlobalIngressIP(existing)
		})

		It("should not reallocate the global IP", func() {
			Consistently(func() string {
				return t.getGlobalIngressIPStatus(existing.Name).AllocatedIP
			}, 200*time.Millisecond).Should(Equal(existing.Status.AllocatedIP))
		})

		It("should not update the Status conditions", func() {
			Consistently(func() int {
				return len(t.getGlobalIngressIPStatus(existing.Name).Conditions)
			}, 200*time.Millisecond).Should(Equal(0))
		})

		It("should reserve the previously allocated IP", func() {
			t.verifyIPsReservedInPool(t.getGlobalIngressIPStatus(existing.Name).AllocatedIP)
		})

		Context("and it's already reserved", func() {
			BeforeEach(func() {
				Expect(t.pool.Reserve(existing.Status.AllocatedIP)).To(Succeed())
			})

			It("should reallocate the global IP", func() {
				t.awaitIngressIPStatus(globalIngressIPName, 0,
					metav1.Condition{
						Type:   string(submarinerv1.GlobalEgressIPAllocated),
						Status: metav1.ConditionFalse,
						Reason: "ReserveAllocatedIPsFailed",
					}, metav1.Condition{
						Type:   string(submarinerv1.GlobalEgressIPAllocated),
						Status: metav1.ConditionTrue,
					})
			})
		})
	})

	Context("with an allocated IP but missing internal service", func() {
		BeforeEach(func() {
			existing.Status.AllocatedIP = globalIP
			exportedService := newClusterIPService()
			t.createService(exportedService)
			t.createGlobalIngressIP(existing)
		})

		It("should release the allocated IP", func() {
			t.awaitIPsReleasedFromPool(existing.Status.AllocatedIP)
		})
	})

	Context("and external IP of internal service does not match with allocated global IP", func() {
		BeforeEach(func() {
			existing.Status.AllocatedIP = "169.254.1.200"
			exportedService := newClusterIPService()
			t.createService(exportedService)

			internalSvc := newGlobalnetInternalService(controllers.GetInternalSvcName(serviceName))
			internalSvc.Spec.ExternalIPs = []string{"169.254.1.111"}
			t.createService(internalSvc)
			t.createGlobalIngressIP(existing)
		})

		It("should successfully create an internal submariner service with valid configuration", func() {
			internalSvcName := controllers.GetInternalSvcName(serviceName)
			intSvc := t.awaitService(internalSvcName)
			externalIP := intSvc.Spec.ExternalIPs[0]
			Expect(externalIP).ToNot(BeEmpty())

			allocatedIP := t.getGlobalIngressIPStatus(globalIngressIPName).AllocatedIP
			Expect(allocatedIP).ToNot(BeEmpty())
			Expect(allocatedIP).To(Equal(externalIP))
		})
	})

	Context("without an allocated IP", func() {
		BeforeEach(func() {
			service := newClusterIPService()
			t.createService(service)
			t.createGlobalIngressIP(existing)
		})

		It("should allocate it", func() {
			t.awaitIngressIPStatusAllocated(globalIngressIPName)
			allocatedIP := t.getGlobalIngressIPStatus(globalIngressIPName).AllocatedIP
			Expect(allocatedIP).ToNot(BeEmpty())
		})

		It("should create an internal submariner service", func() {
			internalSvcName := controllers.GetInternalSvcName(serviceName)
			intSvc := t.awaitService(internalSvcName)
			externalIP := intSvc.Spec.ExternalIPs[0]
			Expect(externalIP).ToNot(BeEmpty())
		})
	})
}

func testExistingGlobalIngressIPHeadlessSvc(t *globalIngressIPControllerTestDriver, ingressIP *submarinerv1.GlobalIngressIP,
	awaitIPTableRules func(string),
) {
	var existing *submarinerv1.GlobalIngressIP

	BeforeEach(func() {
		existing = ingressIP.DeepCopy()
	})

	Context("with an allocated IP", func() {
		BeforeEach(func() {
			existing.Status.AllocatedIP = globalIP
			t.createGlobalIngressIP(existing)
		})

		It("should not reallocate the global IP", func() {
			Consistently(func() string {
				return t.getGlobalIngressIPStatus(existing.Name).AllocatedIP
			}, 200*time.Millisecond).Should(Equal(existing.Status.AllocatedIP))
		})

		It("should not update the Status conditions", func() {
			Consistently(func() int {
				return len(t.getGlobalIngressIPStatus(existing.Name).Conditions)
			}, 200*time.Millisecond).Should(Equal(0))
		})

		It("should reserve the previously allocated IP", func() {
			t.verifyIPsReservedInPool(t.getGlobalIngressIPStatus(existing.Name).AllocatedIP)
		})

		It("should program the relevant IP table rules", func() {
			awaitIPTableRules(existing.Status.AllocatedIP)
		})

		Context("and it's already reserved", func() {
			BeforeEach(func() {
				Expect(t.pool.Reserve(existing.Status.AllocatedIP)).To(Succeed())
			})

			It("should reallocate the global IP", func() {
				t.awaitIngressIPStatus(globalIngressIPName, 0,
					metav1.Condition{
						Type:   string(submarinerv1.GlobalEgressIPAllocated),
						Status: metav1.ConditionFalse,
						Reason: "ReserveAllocatedIPsFailed",
					}, metav1.Condition{
						Type:   string(submarinerv1.GlobalEgressIPAllocated),
						Status: metav1.ConditionTrue,
					})

				awaitIPTableRules(t.getGlobalIngressIPStatus(globalIngressIPName).AllocatedIP)
			})
		})

		Context("and programming the IP table rules fails", func() {
			BeforeEach(func() {
				t.ipt.AddFailOnAppendRuleMatcher(ContainSubstring(existing.Status.AllocatedIP))
			})

			It("should reallocate the global IP", func() {
				t.awaitIngressIPStatus(globalIngressIPName, 0,
					metav1.Condition{
						Type:   string(submarinerv1.GlobalEgressIPAllocated),
						Status: metav1.ConditionFalse,
						Reason: "ReserveAllocatedIPsFailed",
					}, metav1.Condition{
						Type:   string(submarinerv1.GlobalEgressIPAllocated),
						Status: metav1.ConditionTrue,
					})

				allocatedIP := t.getGlobalIngressIPStatus(globalIngressIPName).AllocatedIP
				awaitIPTableRules(allocatedIP)
				t.awaitIPsReleasedFromPool(existing.Status.AllocatedIP)
			})
		})
	})

	Context("without an allocated IP", func() {
		BeforeEach(func() {
			t.createGlobalIngressIP(existing)
		})

		It("should allocate it and program the relevant IP table rules", func() {
			t.awaitIngressIPStatusAllocated(globalIngressIPName)
			awaitIPTableRules(existing.Status.AllocatedIP)
		})
	})
}

type globalIngressIPControllerTestDriver struct {
	*testDriverBase
}

func newGlobalIngressIPControllerDriver() *globalIngressIPControllerTestDriver {
	t := &globalIngressIPControllerTestDriver{}

	BeforeEach(func() {
		t.testDriverBase = newTestDriverBase()

		var err error

		t.pool, err = ipam.NewIPPool(t.globalCIDR)
		Expect(err).To(Succeed())
	})

	JustBeforeEach(func() {
		t.start()
	})

	AfterEach(func() {
		t.testDriverBase.afterEach()
	})

	return t
}

func (t *globalIngressIPControllerTestDriver) start() {
	var err error

	t.controller, err = controllers.NewGlobalIngressIPController(&syncer.ResourceSyncerConfig{
		SourceClient: t.dynClient,
		RestMapper:   t.restMapper,
		Scheme:       t.scheme,
	}, t.pool)

	Expect(err).To(Succeed())
	Expect(t.controller.Start()).To(Succeed())
}

func (t *globalIngressIPControllerTestDriver) awaitPodEgressRules(podIP, snatIP string) {
	t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChainForHeadlessSvcPods, And(ContainSubstring(podIP), ContainSubstring(snatIP)))
}

func (t *globalIngressIPControllerTestDriver) awaitNoPodEgressRules(podIP, snatIP string) {
	t.ipt.AwaitNoRule("nat", constants.SmGlobalnetEgressChainForHeadlessSvcPods, Or(ContainSubstring(podIP), ContainSubstring(snatIP)))
}

func (t *globalIngressIPControllerTestDriver) awaitPodIngressRules(podIP, snatIP string) {
	t.ipt.AwaitRule("nat", constants.SmGlobalnetIngressChain, And(ContainSubstring(podIP), ContainSubstring(snatIP)))
}

func (t *globalIngressIPControllerTestDriver) awaitNoPodIngressRules(podIP, snatIP string) {
	t.ipt.AwaitNoRule("nat", constants.SmGlobalnetIngressChain, Or(ContainSubstring(podIP), ContainSubstring(snatIP)))
}

func (t *globalIngressIPControllerTestDriver) awaitEndpointsEgressRules(endpointsIP, snatIP string) {
	t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChainForHeadlessSvcEPs, And(ContainSubstring(endpointsIP), ContainSubstring(snatIP)))
}

func (t *globalIngressIPControllerTestDriver) awaitNoEndpointsEgressRules(endpointsIP, snatIP string) {
	t.ipt.AwaitNoRule("nat", constants.SmGlobalnetEgressChainForHeadlessSvcEPs, Or(ContainSubstring(endpointsIP), ContainSubstring(snatIP)))
}

func (t *globalIngressIPControllerTestDriver) awaitEndpointsIngressRules(endpointsIP, snatIP string) {
	t.ipt.AwaitRule("nat", constants.SmGlobalnetIngressChain, And(ContainSubstring(endpointsIP), ContainSubstring(snatIP)))
}

func (t *globalIngressIPControllerTestDriver) awaitNoEndpointsIngressRules(endpointsIP, snatIP string) {
	t.ipt.AwaitNoRule("nat", constants.SmGlobalnetIngressChain, Or(ContainSubstring(endpointsIP), ContainSubstring(snatIP)))
}
