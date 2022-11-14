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
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
	"github.com/submariner-io/submariner/pkg/ipam"
	routeAgent "github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Node controller", func() {
	t := newNodeControllerTestDriver()

	var node *corev1.Node

	Context("on startup", func() {
		When("the Node doesn't have a global IP", func() {
			BeforeEach(func() {
				node = t.createNode(nodeName, cniInterfaceIP, "")
			})

			It("should allocate it and program the relevant iptable rules", func() {
				t.awaitIPTableRules(t.awaitNodeGlobalIP(""))
			})

			Context("and the IP pool is initially exhausted", func() {
				var allocatedIPs []string

				BeforeEach(func() {
					allocatedIPs, _ = t.pool.Allocate(t.pool.Size())
				})

				It("should eventually allocate a global IP", func() {
					time.Sleep(time.Millisecond * 300)
					Expect(t.pool.Release(allocatedIPs...)).To(Succeed())

					t.awaitNodeGlobalIP("")
				})
			})
		})

		When("the Node has a global IP", func() {
			BeforeEach(func() {
				node = t.createNode(nodeName, cniInterfaceIP, "169.254.1.100")
			})

			It("should not reallocate it", func() {
				Consistently(func() string {
					obj, err := t.nodes.Get(context.TODO(), nodeName, metav1.GetOptions{})
					Expect(err).To(Succeed())

					return obj.GetAnnotations()[constants.SmGlobalIP]
				}, 200*time.Millisecond).Should(Equal(node.GetAnnotations()[constants.SmGlobalIP]))
			})

			It("should program the relevant iptable rules", func() {
				t.awaitIPTableRules(node.GetAnnotations()[constants.SmGlobalIP])
			})

			It("should reserve the global IP", func() {
				t.verifyIPsReservedInPool(node.GetAnnotations()[constants.SmGlobalIP])
			})

			Context("and it's already reserved", func() {
				BeforeEach(func() {
					Expect(t.pool.Reserve(node.GetAnnotations()[constants.SmGlobalIP])).To(Succeed())
				})

				It("should reallocate the global IP", func() {
					globalIP := t.awaitNodeGlobalIP(node.GetAnnotations()[constants.SmGlobalIP])
					t.awaitIPTableRules(globalIP)
				})
			})
		})
	})

	When("the Node's CNI interface IP is updated", func() {
		Context("without a global IP allocated", func() {
			BeforeEach(func() {
				node = t.createNode(nodeName, "", "")
			})

			JustBeforeEach(func() {
				t.awaitNoNodeGlobalIP()

				addAnnotation(node, routeAgent.CNIInterfaceIP, cniInterfaceIP)
				test.UpdateResource(t.nodes, node)
			})

			It("should allocate a global IP and program the relevant iptable rules", func() {
				t.awaitIPTableRules(t.awaitNodeGlobalIP(""))
			})

			Context("and programming of IP tables initially fails", func() {
				BeforeEach(func() {
					t.ipt.AddFailOnAppendRuleMatcher(ContainSubstring(cniInterfaceIP))
				})

				It("should eventually allocate a global IP and program the relevant iptable rules", func() {
					t.awaitIPTableRules(t.awaitNodeGlobalIP(""))
				})
			})
		})

		Context("with a global IP allocated", func() {
			BeforeEach(func() {
				node = t.createNode(nodeName, "50.60.70.80", "169.254.1.100")
			})

			It("re-program the iptable rules", func() {
				oldCNIIfaceIP := node.GetAnnotations()[routeAgent.CNIInterfaceIP]

				time.Sleep(time.Millisecond * 300)
				addAnnotation(node, routeAgent.CNIInterfaceIP, cniInterfaceIP)
				test.UpdateResource(t.nodes, node)

				t.ipt.AwaitNoRule("nat", constants.SmGlobalnetIngressChain, ContainSubstring(oldCNIIfaceIP))
				t.awaitIPTableRules(node.GetAnnotations()[constants.SmGlobalIP])
				t.verifyIPsReservedInPool(node.GetAnnotations()[constants.SmGlobalIP])
			})
		})
	})

	When("a non-local Node is created and it has a global IP", func() {
		BeforeEach(func() {
			t.createNode(nodeName, "", "169.254.1.100")
			node = t.createNode("otherNode", cniInterfaceIP, "169.254.1.100")
			_ = t.pool.Reserve(node.GetAnnotations()[constants.SmGlobalIP])
		})

		It("should release the global IP", func() {
			t.awaitIPsReleasedFromPool(node.GetAnnotations()[constants.SmGlobalIP])
			Eventually(func() string {
				obj := test.GetResource(t.nodes, node)
				return obj.GetAnnotations()[constants.SmGlobalIP]
			}).Should(BeEmpty())
		})
	})
})

type nodeControllerTestDriver struct {
	*testDriverBase
}

func newNodeControllerTestDriver() *nodeControllerTestDriver {
	t := &nodeControllerTestDriver{}

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

func (t *nodeControllerTestDriver) start() {
	var err error

	t.controller, err = controllers.NewNodeController(&syncer.ResourceSyncerConfig{
		SourceClient: t.dynClient,
		RestMapper:   t.restMapper,
		Scheme:       t.scheme,
	}, t.pool, nodeName)

	Expect(err).To(Succeed())
	Expect(t.controller.Start()).To(Succeed())
}

func (t *nodeControllerTestDriver) awaitIPTableRules(globalIP string) {
	t.ipt.AwaitRule("nat", constants.SmGlobalnetIngressChain, And(ContainSubstring(globalIP), ContainSubstring(cniInterfaceIP)))
}
