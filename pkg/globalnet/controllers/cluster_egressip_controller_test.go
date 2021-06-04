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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("ClusterGlobalEgressIP controller", func() {
	t := newClusterGlobalEgressIPControllerTestDriver()

	When("the well-known ClusterGlobalEgressIP does not exist on startup", func() {
		It("should create it and allocate the global IP", func() {
			t.awaitClusterGlobalEgressIPStatusAllocated(controllers.ClusterGlobalEgressIPName, 1)
			// TODO - verify IP tables
		})
	})

	When("the well-known ClusterGlobalEgressIP exists on startup", func() {
		var existing *submarinerv1.ClusterGlobalEgressIP

		BeforeEach(func() {
			existing = newClusterGlobalEgressIP(controllers.ClusterGlobalEgressIPName, 3)
			existing.Status.AllocatedIPs = []string{"169.254.1.100", "169.254.1.101", "169.254.1.102"}
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
			t.verifyIPsReservedInPool(getGlobalEgressIPStatus(t.clusterGlobalEgressIPs, existing.Name).AllocatedIPs...)
		})
	})

	When("a ClusterGlobalEgressIP is created without the well-known name", func() {
		JustBeforeEach(func() {
			t.createClusterGlobalEgressIP(newClusterGlobalEgressIP("other name", 1))
		})

		It("should not allocate the global IP", func() {
			t.awaitGlobalEgressIPStatus(t.clusterGlobalEgressIPs, "other name", t.globalCIDR,
				0, 0, metav1.Condition{
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

	t.pool, err = ipam.NewIPPool(t.globalCIDR)
	Expect(err).To(Succeed())

	t.controller, err = controllers.NewClusterGlobalEgressIPController(syncer.ResourceSyncerConfig{
		SourceClient: t.dynClient,
		RestMapper:   t.restMapper,
		Scheme:       t.scheme,
	}, t.pool)

	Expect(err).To(Succeed())
	Expect(t.controller.Start()).To(Succeed())
}
