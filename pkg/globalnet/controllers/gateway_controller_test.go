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
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
	"github.com/submariner-io/submariner/pkg/ipam"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Node controller", func() {
	t := newNodeControllerTestDriver()

	var node *corev1.Node

	Context("on startup", func() {
		When("the Node doesn't have a global IP", func() {
			BeforeEach(func() {
				node = t.createNode(nodeName, "")
			})

			It("should allocate it and program the relevant iptable rules", func() {
				t.awaitPacketFilterRules(t.awaitNodeGlobalIP(""))
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
				node = t.createNode(nodeName, globalIP1)
			})

			It("should not reallocate it", func() {
				Consistently(func() string {
					obj, err := t.nodes.Get(context.TODO(), nodeName, metav1.GetOptions{})
					Expect(err).To(Succeed())

					return obj.GetAnnotations()[constants.SmGlobalIP]
				}, 200*time.Millisecond).Should(Equal(node.GetAnnotations()[constants.SmGlobalIP]))
			})

			It("should program the relevant iptable rules", func() {
				t.awaitPacketFilterRules(node.GetAnnotations()[constants.SmGlobalIP])
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
					t.awaitPacketFilterRules(globalIP)
				})
			})
		})

		When("the CNI IP discovery fails", func() {
			BeforeEach(func() {
				t.localCIDRs = []string{"10.128.1.0/16"}
				node = t.createNode(nodeName, "")
			})

			It("should not allocate a global IP", func() {
				t.ensureNoNodeGlobalIP()
			})
		})
	})

	When("a non-local Node is created and it has a global IP", func() {
		BeforeEach(func() {
			t.createNode(nodeName, globalIP1)
			node = t.createNode("otherNode", globalIP1)
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
	localCIDRs []string
}

func newNodeControllerTestDriver() *nodeControllerTestDriver {
	t := &nodeControllerTestDriver{}

	BeforeEach(func() {
		t.testDriverBase = newTestDriverBase()
		t.testDriverBase.initChains()

		var err error

		t.pool, err = ipam.NewIPPool(t.globalCIDR)
		Expect(err).To(Succeed())

		t.localCIDRs = []string{localCIDR}
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
	}, t.pool, nodeName, t.localCIDRs)

	Expect(err).To(Succeed())
	Expect(t.controller.Start()).To(Succeed())
}

func (t *nodeControllerTestDriver) awaitPacketFilterRules(globalIP string) {
	t.pFilter.AwaitRule(packetfilter.TableTypeNAT,
		constants.SmGlobalnetIngressChain, And(ContainSubstring(globalIP), ContainSubstring(cniInterfaceIP)))
}
