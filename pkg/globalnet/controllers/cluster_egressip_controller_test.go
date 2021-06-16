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
	"github.com/submariner-io/admiral/pkg/syncer/test"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
	"github.com/submariner-io/submariner/pkg/ipam"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("ClusterGlobalEgressIP controller", func() {
	t := newClusterGlobalEgressIPControllerTestDriver()

	When("the well-known ClusterGlobalEgressIP does not exist on startup", func() {
		It("should create it and allocate the global IP", func() {
			t.awaitClusterGlobalEgressIPStatusAllocated(1)
			t.awaitIPTableRules(getGlobalEgressIPStatus(t.clusterGlobalEgressIPs, controllers.ClusterGlobalEgressIPName).AllocatedIPs...)
		})
	})

	When("the well-known ClusterGlobalEgressIP exists on startup", func() {
		Context("with allocated IPs", func() {
			var existing *submarinerv1.ClusterGlobalEgressIP

			BeforeEach(func() {
				existing = newClusterGlobalEgressIP(controllers.ClusterGlobalEgressIPName, 3)
				existing.Status.AllocatedIPs = []string{"169.254.1.100", "169.254.1.101", "169.254.1.102"}
			})

			Context("and the NumberOfIPs unchanged", func() {
				BeforeEach(func() {
					t.createClusterGlobalEgressIP(existing)
				})

				It("should not reallocate the global IPs", func() {
					Consistently(func() []string {
						return getGlobalEgressIPStatus(t.clusterGlobalEgressIPs, existing.Name).AllocatedIPs
					}, 200*time.Millisecond).Should(Equal(existing.Status.AllocatedIPs))
				})

				It("should not update the Status conditions", func() {
					Consistently(func() int {
						return len(getGlobalEgressIPStatus(t.clusterGlobalEgressIPs, existing.Name).Conditions)
					}, 200*time.Millisecond).Should(Equal(0))
				})

				It("should reserve the previously allocated IPs", func() {
					t.verifyIPsReservedInPool(existing.Status.AllocatedIPs...)
				})

				It("should program the necessary IP table rules for the allocated IPs", func() {
					t.awaitIPTableRules(existing.Status.AllocatedIPs...)
				})
			})

			Context("and the NumberOfIPs changed", func() {
				BeforeEach(func() {
					n := *existing.Spec.NumberOfIPs + 1
					existing.Spec.NumberOfIPs = &n
					t.createClusterGlobalEgressIP(existing)
				})

				It("should reallocate the global IPs", func() {
					t.awaitClusterGlobalEgressIPStatusAllocated(*existing.Spec.NumberOfIPs)
					t.awaitIPTableRules(getGlobalEgressIPStatus(t.clusterGlobalEgressIPs, controllers.ClusterGlobalEgressIPName).AllocatedIPs...)
				})

				It("should release the previously allocated IPs", func() {
					t.awaitIPsReleasedFromPool(existing.Status.AllocatedIPs...)
					t.awaitNoIPTableRules(existing.Status.AllocatedIPs...)
				})
			})

			Context("and they're already reserved", func() {
				BeforeEach(func() {
					existing.Status.Conditions = []metav1.Condition{
						{
							Type:    string(submarinerv1.GlobalEgressIPAllocated),
							Status:  metav1.ConditionTrue,
							Reason:  "Success",
							Message: "Allocated global IPs",
						},
					}

					t.createClusterGlobalEgressIP(existing)
					Expect(t.pool.Reserve(existing.Status.AllocatedIPs...)).To(Succeed())
				})

				It("should reallocate the global IPs", func() {
					t.awaitEgressIPStatus(t.clusterGlobalEgressIPs, controllers.ClusterGlobalEgressIPName, *existing.Spec.NumberOfIPs, 1,
						metav1.Condition{
							Type:   string(submarinerv1.GlobalEgressIPAllocated),
							Status: metav1.ConditionFalse,
							Reason: "ReserveAllocatedIPsFailed",
						}, metav1.Condition{
							Type:   string(submarinerv1.GlobalEgressIPAllocated),
							Status: metav1.ConditionTrue,
						})

					t.awaitIPTableRules(getGlobalEgressIPStatus(t.clusterGlobalEgressIPs, controllers.ClusterGlobalEgressIPName).AllocatedIPs...)
				})
			})

			Context("and programming the IP table rules fails", func() {
				BeforeEach(func() {
					t.createClusterGlobalEgressIP(existing)
					t.ipt.AddFailOnAppendRuleMatcher(ContainSubstring(existing.Status.AllocatedIPs[0]))
				})

				It("should reallocate the global IPs", func() {
					t.awaitEgressIPStatus(t.clusterGlobalEgressIPs, controllers.ClusterGlobalEgressIPName, *existing.Spec.NumberOfIPs, 0,
						metav1.Condition{
							Type:   string(submarinerv1.GlobalEgressIPAllocated),
							Status: metav1.ConditionFalse,
							Reason: "ReserveAllocatedIPsFailed",
						}, metav1.Condition{
							Type:   string(submarinerv1.GlobalEgressIPAllocated),
							Status: metav1.ConditionTrue,
						})

					allocatedIPs := getGlobalEgressIPStatus(t.clusterGlobalEgressIPs, controllers.ClusterGlobalEgressIPName).AllocatedIPs
					t.awaitIPTableRules(allocatedIPs...)
					t.awaitIPsReleasedFromPool(existing.Status.AllocatedIPs...)
				})
			})
		})

		Context("with the NumberOfIPs negative", func() {
			BeforeEach(func() {
				t.createClusterGlobalEgressIP(newClusterGlobalEgressIP(controllers.ClusterGlobalEgressIPName, -1))
			})

			It("should add an appropriate Status condition", func() {
				t.awaitEgressIPStatus(t.clusterGlobalEgressIPs, controllers.ClusterGlobalEgressIPName, 0, 0, metav1.Condition{
					Type:   string(submarinerv1.GlobalEgressIPAllocated),
					Status: metav1.ConditionFalse,
					Reason: "InvalidInput",
				})
			})
		})

		Context("with the NumberOfIPs zero", func() {
			BeforeEach(func() {
				t.createClusterGlobalEgressIP(newClusterGlobalEgressIP(controllers.ClusterGlobalEgressIPName, 0))
			})

			It("should add an appropriate Status condition", func() {
				t.awaitEgressIPStatus(t.clusterGlobalEgressIPs, controllers.ClusterGlobalEgressIPName, 0, 0, metav1.Condition{
					Type:   string(submarinerv1.GlobalEgressIPAllocated),
					Status: metav1.ConditionFalse,
					Reason: "ZeroInput",
				})
			})
		})
	})

	When("the well-known ClusterGlobalEgressIP is updated", func() {
		var existing *submarinerv1.ClusterGlobalEgressIP
		var numberOfIPs int

		BeforeEach(func() {
			existing = newClusterGlobalEgressIP(controllers.ClusterGlobalEgressIPName, 2)
			existing.Status.AllocatedIPs = []string{"169.254.1.100", "169.254.1.101"}
			t.createClusterGlobalEgressIP(existing)
		})

		JustBeforeEach(func() {
			existing.Spec.NumberOfIPs = &numberOfIPs
			test.UpdateResource(t.clusterGlobalEgressIPs, existing)
		})

		Context("with the NumberOfIPs greater", func() {
			BeforeEach(func() {
				numberOfIPs = *existing.Spec.NumberOfIPs + 1
			})

			It("should reallocate the global IPs", func() {
				t.awaitClusterGlobalEgressIPStatusAllocated(numberOfIPs)
				t.awaitIPTableRules(getGlobalEgressIPStatus(t.clusterGlobalEgressIPs, controllers.ClusterGlobalEgressIPName).AllocatedIPs...)
			})

			It("should release the previously allocated IPs", func() {
				t.awaitIPsReleasedFromPool(existing.Status.AllocatedIPs...)
				t.awaitNoIPTableRules(existing.Status.AllocatedIPs...)
			})
		})

		Context("with the NumberOfIPs less", func() {
			BeforeEach(func() {
				numberOfIPs = *existing.Spec.NumberOfIPs - 1
			})

			It("should reallocate the global IPs", func() {
				t.awaitClusterGlobalEgressIPStatusAllocated(numberOfIPs)
				t.awaitIPTableRules(getGlobalEgressIPStatus(t.clusterGlobalEgressIPs, controllers.ClusterGlobalEgressIPName).AllocatedIPs...)
			})

			It("should release the previously allocated IPs", func() {
				t.awaitIPsReleasedFromPool(existing.Status.AllocatedIPs...)
				t.awaitNoIPTableRules(existing.Status.AllocatedIPs...)
			})
		})

		Context("with the NumberOfIPs zero", func() {
			BeforeEach(func() {
				numberOfIPs = 0
			})

			It("should update the Status appropriately", func() {
				t.awaitEgressIPStatus(t.clusterGlobalEgressIPs, controllers.ClusterGlobalEgressIPName, 0, 0, metav1.Condition{
					Type:   string(submarinerv1.GlobalEgressIPAllocated),
					Status: metav1.ConditionFalse,
					Reason: "ZeroInput",
				})
			})

			It("should release the previously allocated IPs", func() {
				t.awaitIPsReleasedFromPool(existing.Status.AllocatedIPs...)
				t.awaitNoIPTableRules(existing.Status.AllocatedIPs...)
			})
		})

		Context("and IP tables cleanup of previously allocated IPs initially fails", func() {
			BeforeEach(func() {
				numberOfIPs = *existing.Spec.NumberOfIPs + 1
				t.ipt.AddFailOnDeleteRuleMatcher(ContainSubstring(existing.Status.AllocatedIPs[0]))
			})

			It("should eventually cleanup the IP tables and reallocate", func() {
				t.awaitNoIPTableRules(existing.Status.AllocatedIPs...)
				t.awaitClusterGlobalEgressIPStatusAllocated(numberOfIPs)
				t.awaitIPTableRules(getGlobalEgressIPStatus(t.clusterGlobalEgressIPs, controllers.ClusterGlobalEgressIPName).AllocatedIPs...)
			})
		})

		Context("and programming of IP tables initially fails", func() {
			BeforeEach(func() {
				numberOfIPs = *existing.Spec.NumberOfIPs + 1
				t.ipt.AddFailOnAppendRuleMatcher(Not(ContainSubstring(existing.Status.AllocatedIPs[0])))
			})

			It("should eventually reallocate the global IPs", func() {
				t.awaitNoIPTableRules(existing.Status.AllocatedIPs...)
				t.awaitEgressIPStatus(t.clusterGlobalEgressIPs, controllers.ClusterGlobalEgressIPName, numberOfIPs, 0, metav1.Condition{
					Type:   string(submarinerv1.GlobalEgressIPAllocated),
					Status: metav1.ConditionFalse,
					Reason: "ProgramIPTableRulesFailed",
				}, metav1.Condition{
					Type:   string(submarinerv1.GlobalEgressIPAllocated),
					Status: metav1.ConditionTrue,
				})
				t.awaitIPTableRules(getGlobalEgressIPStatus(t.clusterGlobalEgressIPs, controllers.ClusterGlobalEgressIPName).AllocatedIPs...)
			})
		})

		Context("with the IP pool exhausted", func() {
			BeforeEach(func() {
				numberOfIPs = t.pool.Size() + 1
			})

			It("should add an appropriate Status condition", func() {
				awaitStatusConditions(t.clusterGlobalEgressIPs, controllers.ClusterGlobalEgressIPName, 0, metav1.Condition{
					Type:   string(submarinerv1.GlobalEgressIPAllocated),
					Status: metav1.ConditionFalse,
					Reason: "IPPoolAllocationFailed",
				})
			})
		})
	})

	When("the well-known ClusterGlobalEgressIP is deleted", func() {
		var allocatedIPs []string

		BeforeEach(func() {
			t.createClusterGlobalEgressIP(newClusterGlobalEgressIP(controllers.ClusterGlobalEgressIPName, 1))
		})

		JustBeforeEach(func() {
			t.awaitClusterGlobalEgressIPStatusAllocated(1)
			allocatedIPs = getGlobalEgressIPStatus(t.clusterGlobalEgressIPs, controllers.ClusterGlobalEgressIPName).AllocatedIPs
			Expect(t.clusterGlobalEgressIPs.Delete(context.TODO(), controllers.ClusterGlobalEgressIPName, metav1.DeleteOptions{}))
		})

		It("should release the previously allocated IPs", func() {
			t.awaitIPsReleasedFromPool(allocatedIPs...)
		})
	})

	When("a ClusterGlobalEgressIP is created without the well-known name", func() {
		JustBeforeEach(func() {
			t.createClusterGlobalEgressIP(newClusterGlobalEgressIP("other name", 1))
		})

		It("should not allocate the global IP", func() {
			t.awaitEgressIPStatus(t.clusterGlobalEgressIPs, "other name", 0, 0, metav1.Condition{
				Type:   string(submarinerv1.GlobalEgressIPAllocated),
				Status: metav1.ConditionFalse,
				Reason: "InvalidInstance",
			})
		})
	})
})

type clusterGlobalEgressIPControllerTestDriver struct {
	*testDriverBase
}

func newClusterGlobalEgressIPControllerTestDriver() *clusterGlobalEgressIPControllerTestDriver {
	t := &clusterGlobalEgressIPControllerTestDriver{}

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

func (t *clusterGlobalEgressIPControllerTestDriver) start() {
	var err error

	t.localSubnets = []string{"10.0.0.0/16", "100.0.0.0/16"}

	t.controller, err = controllers.NewClusterGlobalEgressIPController(syncer.ResourceSyncerConfig{
		SourceClient: t.dynClient,
		RestMapper:   t.restMapper,
		Scheme:       t.scheme,
	}, t.localSubnets, t.pool)

	Expect(err).To(Succeed())
	Expect(t.controller.Start()).To(Succeed())
}

func (t *clusterGlobalEgressIPControllerTestDriver) awaitIPTableRules(ips ...string) {
	targetSNATIP := ips[0]
	if len(ips) > 1 {
		targetSNATIP += "-" + ips[len(ips)-1]
	}

	t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChainForCluster, ContainSubstring(targetSNATIP))

	for _, localSubnet := range t.localSubnets {
		t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChainForCluster, ContainSubstring(localSubnet))
	}
}

func (t *clusterGlobalEgressIPControllerTestDriver) awaitNoIPTableRules(ips ...string) {
	targetSNATIP := ips[0]
	if len(ips) > 1 {
		targetSNATIP += "-" + ips[len(ips)-1]
	}

	t.ipt.AwaitNoRule("nat", constants.SmGlobalnetEgressChainForCluster, ContainSubstring(targetSNATIP))
}
