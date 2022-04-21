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
package globalnetdataplane_test

// import (
// 	"context"

// . "github.com/onsi/ginkgo"
// . "github.com/onsi/gomega"
// "k8s.io/klog"
// "github.com/submariner-io/submariner/pkg/globalnet/constants"
// "github.com/submariner-io/submariner/pkg/globalnet/controllers"
// 	"github.com/submariner-io/admiral/pkg/syncer"
// 	"github.com/submariner-io/admiral/pkg/syncer/test"
// 	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
// 	"github.com/submariner-io/submariner/pkg/globalnet/constants"
// 	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
// 	"github.com/submariner-io/submariner/pkg/ipam"
// 	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
// )

// var _ = Describe("ClusterGlobalEgressIP controller", func() {
// 	t := newClusterGlobalEgressIPControllerTestDriver()

// When("the well-known ClusterGlobalEgressIP does not exist on startup", func() {
// 	It("should create it and allocate the default number of global IPs", func() {
// 		t.awaitClusterGlobalEgressIPStatusAllocated(controllers.DefaultNumberOfClusterEgressIPs)
// 		t.awaitIPTableRules(getGlobalEgressIPStatus(t.clusterGlobalEgressIPs, constants.ClusterGlobalEgressIPName).AllocatedIPs...)
// 	})
// })
// 	When("the well-known ClusterGlobalEgressIP exists on startup", func() {
// 		Context("with allocated IPs", func() {
// 			var existing *submarinerv1.ClusterGlobalEgressIP

// 			BeforeEach(func() {
// 				existing = newClusterGlobalEgressIP(constants.ClusterGlobalEgressIPName, 3)
// 				existing.Status.AllocatedIPs = []string{"169.254.1.100", "169.254.1.101", "169.254.1.102"}
// 			})

// 			Context("and programming the IP table rules fails", func() {
// 				BeforeEach(func() {
// 					t.createClusterGlobalEgressIP(existing)
// 					t.ipt.AddFailOnAppendRuleMatcher(ContainSubstring(existing.Status.AllocatedIPs[0]))
// 				})

// 				It("should reallocate the global IPs", func() {
// 					t.awaitEgressIPStatus(t.clusterGlobalEgressIPs, constants.ClusterGlobalEgressIPName, *existing.Spec.NumberOfIPs, 0,
// 						metav1.Condition{
// 							Type:   string(submarinerv1.GlobalEgressIPAllocated),
// 							Status: metav1.ConditionFalse,
// 							Reason: "ReserveAllocatedIPsFailed",
// 						}, metav1.Condition{
// 							Type:   string(submarinerv1.GlobalEgressIPAllocated),
// 							Status: metav1.ConditionTrue,
// 						})

// 					allocatedIPs := getGlobalEgressIPStatus(t.clusterGlobalEgressIPs, constants.ClusterGlobalEgressIPName).AllocatedIPs
// 					t.awaitIPTableRules(allocatedIPs...)
// 					t.awaitIPsReleasedFromPool(existing.Status.AllocatedIPs...)
// 				})
// 			})
// 		})

// 		Context("with the NumberOfIPs negative", func() {
// 			BeforeEach(func() {
// 				t.createClusterGlobalEgressIP(newClusterGlobalEgressIP(constants.ClusterGlobalEgressIPName, -1))
// 			})

// 			It("should add an appropriate Status condition", func() {
// 				t.awaitEgressIPStatus(t.clusterGlobalEgressIPs, constants.ClusterGlobalEgressIPName, 0, 0, metav1.Condition{
// 					Type:   string(submarinerv1.GlobalEgressIPAllocated),
// 					Status: metav1.ConditionFalse,
// 					Reason: "InvalidInput",
// 				})
// 			})
// 		})

// 		Context("with the NumberOfIPs zero", func() {
// 			BeforeEach(func() {
// 				t.createClusterGlobalEgressIP(newClusterGlobalEgressIP(constants.ClusterGlobalEgressIPName, 0))
// 			})

// 			It("should add an appropriate Status condition", func() {
// 				t.awaitEgressIPStatus(t.clusterGlobalEgressIPs, constants.ClusterGlobalEgressIPName, 0, 0, metav1.Condition{
// 					Type:   string(submarinerv1.GlobalEgressIPAllocated),
// 					Status: metav1.ConditionFalse,
// 					Reason: "ZeroInput",
// 				})
// 			})
// 		})
// 	})

// 	When("the well-known ClusterGlobalEgressIP is updated", func() {
// 		var existing *submarinerv1.ClusterGlobalEgressIP
// 		var numberOfIPs int

// 		BeforeEach(func() {
// 			existing = newClusterGlobalEgressIP(constants.ClusterGlobalEgressIPName, 2)
// 			existing.Status.AllocatedIPs = []string{"169.254.1.100", "169.254.1.101"}
// 			t.createClusterGlobalEgressIP(existing)
// 		})

// 		JustBeforeEach(func() {
// 			existing.Spec.NumberOfIPs = &numberOfIPs
// 			test.UpdateResource(t.clusterGlobalEgressIPs, existing)
// 		})

// 		Context("and IP tables cleanup of previously allocated IPs initially fails", func() {
// 			BeforeEach(func() {
// 				numberOfIPs = *existing.Spec.NumberOfIPs + 1
// 				t.ipt.AddFailOnDeleteRuleMatcher(ContainSubstring(existing.Status.AllocatedIPs[0]))
// 			})

// 			It("should eventually cleanup the IP tables and reallocate", func() {
// 				t.awaitNoIPTableRules(existing.Status.AllocatedIPs...)
// 				t.awaitClusterGlobalEgressIPStatusAllocated(numberOfIPs)
// 				t.awaitIPTableRules(getGlobalEgressIPStatus(t.clusterGlobalEgressIPs, constants.ClusterGlobalEgressIPName).AllocatedIPs...)
// 			})
// 		})

// 		Context("and programming of IP tables initially fails", func() {
// 			BeforeEach(func() {
// 				numberOfIPs = *existing.Spec.NumberOfIPs + 1
// 				t.ipt.AddFailOnAppendRuleMatcher(Not(ContainSubstring(existing.Status.AllocatedIPs[0])))
// 			})

// 			It("should eventually reallocate the global IPs", func() {
// 				t.awaitNoIPTableRules(existing.Status.AllocatedIPs...)
// 				t.awaitEgressIPStatus(t.clusterGlobalEgressIPs, constants.ClusterGlobalEgressIPName, numberOfIPs, 0, metav1.Condition{
// 					Type:   string(submarinerv1.GlobalEgressIPAllocated),
// 					Status: metav1.ConditionFalse,
// 					Reason: "ProgramIPTableRulesFailed",
// 				}, metav1.Condition{
// 					Type:   string(submarinerv1.GlobalEgressIPAllocated),
// 					Status: metav1.ConditionTrue,
// 				})
// 				t.awaitIPTableRules(getGlobalEgressIPStatus(t.clusterGlobalEgressIPs, constants.ClusterGlobalEgressIPName).AllocatedIPs...)
// 			})
// 		})

// 	})

// 	When("the well-known ClusterGlobalEgressIP is deleted", func() {
// 		var allocatedIPs []string

// 		BeforeEach(func() {
// 			t.createClusterGlobalEgressIP(newClusterGlobalEgressIP(constants.ClusterGlobalEgressIPName, 1))
// 		})

// 		JustBeforeEach(func() {
// 			t.awaitClusterGlobalEgressIPStatusAllocated(1)
// 			allocatedIPs = getGlobalEgressIPStatus(t.clusterGlobalEgressIPs, constants.ClusterGlobalEgressIPName).AllocatedIPs
// 			Expect(t.clusterGlobalEgressIPs.Delete(context.TODO(), constants.ClusterGlobalEgressIPName, metav1.DeleteOptions{}))
// 		})

// 		It("should cleanup the previously written IPtable rules", func() {
// 			t.awaitIPsReleasedFromPool(allocatedIPs...)
// 		})
// 	})

// 	When("a ClusterGlobalEgressIP is created without the well-known name", func() {
// 		JustBeforeEach(func() {
// 			t.createClusterGlobalEgressIP(newClusterGlobalEgressIP("other name", 1))
// 		})

// 		It("should not allocate the global IP", func() {
// 			t.awaitEgressIPStatus(t.clusterGlobalEgressIPs, "other name", 0, 0, metav1.Condition{
// 				Type:   string(submarinerv1.GlobalEgressIPAllocated),
// 				Status: metav1.ConditionFalse,
// 				Reason: "InvalidInstance",
// 			})
// 		})
// 	})
// })

// type clusterGlobalEgressIPControllerTestDriver struct {
// 	*testDriverBase
// }

// func newClusterGlobalEgressIPControllerTestDriver() *clusterGlobalEgressIPControllerTestDriver {
// 	t := &clusterGlobalEgressIPControllerTestDriver{}

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

// func (t *clusterGlobalEgressIPControllerTestDriver) start() {
// 	var err error

// 	t.localSubnets = []string{"10.0.0.0/16", "100.0.0.0/16"}

// 	t.controller, err = controllers.NewClusterGlobalEgressIPController(&syncer.ResourceSyncerConfig{
// 		SourceClient: t.dynClient,
// 		RestMapper:   t.restMapper,
// 		Scheme:       t.scheme,
// 	}, t.localSubnets, t.pool)

// 	Expect(err).To(Succeed())
// 	Expect(t.controller.Start()).To(Succeed())
// }

// func (t *clusterGlobalEgressIPControllerTestDriver) awaitIPTableRules(ips ...string) {
// 	t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChainForCluster, ContainSubstring(getSNATAddress(ips...)))

// 	for _, localSubnet := range t.localSubnets {
// 		t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChainForCluster, ContainSubstring(localSubnet))
// 	}
// }

// func (t *clusterGlobalEgressIPControllerTestDriver) awaitNoIPTableRules(ips ...string) {
// 	t.ipt.AwaitNoRule("nat", constants.SmGlobalnetEgressChainForCluster, ContainSubstring(getSNATAddress(ips...)))
// })
