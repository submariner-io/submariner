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
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
	"github.com/submariner-io/submariner/pkg/ipam"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Gateway controller", func() {
	t := newGatewayControllerTestDriver()

	var gateway *submarinerv1.Gateway

	Context("on startup", func() {
		When("the Gateway doesn't have a global IP", func() {
			BeforeEach(func() {
				gateway = t.createGateway(t.hostName, "")
			})

			It("should allocate it and program the relevant iptable rules", func() {
				t.awaitPacketFilterRules(t.awaitGatewayGlobalIP(""))
			})

			Context("and the IP pool is initially exhausted", func() {
				var allocatedIPs []string

				BeforeEach(func() {
					allocatedIPs, _ = t.pool.Allocate(t.pool.Size())
				})

				It("should eventually allocate a global IP", func() {
					time.Sleep(time.Millisecond * 300)
					Expect(t.pool.Release(allocatedIPs...)).To(Succeed())

					t.awaitGatewayGlobalIP("")
				})
			})
		})

		When("the Gateway has a global IP", func() {
			BeforeEach(func() {
				gateway = t.createGateway(t.hostName, globalIP1)
				t.expectReservedIPs = []string{gateway.GetAnnotations()[constants.SmGlobalIP]}
			})

			It("should not reallocate it", func() {
				Consistently(func() string {
					obj, err := t.gateways.Get(context.TODO(), t.hostName, metav1.GetOptions{})
					Expect(err).To(Succeed())

					return obj.GetAnnotations()[constants.SmGlobalIP]
				}, 200*time.Millisecond).Should(Equal(gateway.GetAnnotations()[constants.SmGlobalIP]))
			})

			It("should program the relevant iptable rules", func() {
				t.awaitPacketFilterRules(gateway.GetAnnotations()[constants.SmGlobalIP])
			})

			Context("and it's already reserved", func() {
				BeforeEach(func() {
					Expect(t.pool.Reserve(gateway.GetAnnotations()[constants.SmGlobalIP])).To(Succeed())
				})

				It("should reallocate the global IP", func() {
					globalIP := t.awaitGatewayGlobalIP(gateway.GetAnnotations()[constants.SmGlobalIP])
					t.awaitPacketFilterRules(globalIP)
				})
			})
		})
	})

	When("a non-active Gateway exists on startup and has a global IP", func() {
		BeforeEach(func() {
			gateway = t.createGateway("other-gateway", globalIP2)
			t.expectReservedIPs = []string{gateway.GetAnnotations()[constants.SmGlobalIP]}
		})

		It("should reserve the global IP and preserve the annotation", func() {
			t.ensureNoPacketFilterRules(gateway.GetAnnotations()[constants.SmGlobalIP])
			t.ensureGatewayGlobalIP(gateway.Name, gateway.GetAnnotations()[constants.SmGlobalIP])
		})

		Context("and is subsequently deleted", func() {
			It("should release its global IP", func() {
				err := t.gateways.Delete(context.TODO(), gateway.Name, metav1.DeleteOptions{})
				Expect(err).To(Succeed())

				t.awaitIPsReleasedFromPool(gateway.GetAnnotations()[constants.SmGlobalIP])
			})
		})

		Context("and it's already reserved", func() {
			BeforeEach(func() {
				Expect(t.pool.Reserve(gateway.GetAnnotations()[constants.SmGlobalIP])).To(Succeed())
			})

			It("should preserve the annotation", func() {
				t.ensureNoPacketFilterRules(gateway.GetAnnotations()[constants.SmGlobalIP])
				t.ensureGatewayGlobalIP(gateway.Name, gateway.GetAnnotations()[constants.SmGlobalIP])
			})
		})
	})
})

type gatewayControllerTestDriver struct {
	*testDriverBase
	localCIDRs        []string
	expectReservedIPs []string
}

func newGatewayControllerTestDriver() *gatewayControllerTestDriver {
	t := &gatewayControllerTestDriver{}

	BeforeEach(func() {
		t.testDriverBase = newTestDriverBase()
		t.testDriverBase.initChains()

		var err error

		t.pool, err = ipam.NewIPPool(t.globalCIDR)
		Expect(err).To(Succeed())

		t.localCIDRs = []string{localCIDR}
		t.expectReservedIPs = nil
	})

	JustBeforeEach(func() {
		t.start()
	})

	AfterEach(func() {
		t.testDriverBase.afterEach()
	})

	return t
}

func (t *gatewayControllerTestDriver) start() {
	var err error

	syncerConfig := controllers.NewGatewayResourceSyncerConfig(&syncer.ResourceSyncerConfig{
		SourceClient: t.dynClient,
		RestMapper:   t.restMapper,
		Scheme:       t.scheme,
	}, namespace)

	informer, err := syncer.NewSharedInformer(syncerConfig)
	Expect(err).To(Succeed())

	stopCh := make(chan struct{})

	DeferCleanup(func() {
		close(stopCh)
	})

	t.controller, err = controllers.NewGatewayController(syncerConfig, informer, t.pool, t.hostName, namespace, cniInterfaceIP)
	Expect(err).To(Succeed())

	t.verifyIPsReservedInPool(t.expectReservedIPs...)

	go func() {
		informer.Run(stopCh)
	}()

	Expect(t.controller.Start()).To(Succeed())
}

func (t *gatewayControllerTestDriver) awaitPacketFilterRules(globalIP string) {
	t.pFilter.AwaitRule(packetfilter.TableTypeNAT,
		constants.SmGlobalnetIngressChain, And(ContainSubstring(globalIP), ContainSubstring(cniInterfaceIP)))
}

func (t *gatewayControllerTestDriver) ensureNoPacketFilterRules(globalIP string) {
	t.pFilter.EnsureNoRule(packetfilter.TableTypeNAT,
		constants.SmGlobalnetIngressChain, And(ContainSubstring(globalIP), ContainSubstring(cniInterfaceIP)))
}
