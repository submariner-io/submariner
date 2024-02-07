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

package calico_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	calicoapi "github.com/projectcalico/api/pkg/apis/projectcalico/v3"
	"github.com/submariner-io/submariner/pkg/event/testing"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/calico"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("IPPool Handler", func() {
	t := newTestDriver()

	JustBeforeEach(func() {
		t.Start(calico.NewCalicoIPPoolHandler(nil, testing.Namespace, t.k8sClient))
	})

	When("remote Endpoints are created and deleted", func() {
		It("should create and delete IPPools", func() {
			subnets1 := []string{"192.0.2.0/24", "192.0.3.0/24"}
			remoteEP1 := t.CreateEndpoint(testing.NewEndpoint("remote-cluster1", "host", subnets1...))
			t.ensureNoIPPools(subnets1...)

			localEP := t.CreateLocalHostEndpoint()
			t.awaitIPPools(subnets1...)

			// Ensure it handles existing IPPools.
			Expect(t.handler.RemoteEndpointCreated(remoteEP1)).To(Succeed())

			subnets2 := []string{"192.0.4.0/24"}
			remoteEP2 := t.CreateEndpoint(testing.NewEndpoint("remote-cluster1", "host", subnets2...))
			t.awaitIPPools(subnets2...)

			t.DeleteEndpoint(remoteEP1.Name)
			t.awaitNoIPPools(subnets1...)

			t.DeleteEndpoint(localEP.Name)

			t.DeleteEndpoint(remoteEP2.Name)
			t.ensureIPPools(subnets2...)
		})
	})

	When("the platform is not ROKS", func() {
		Context("because the Submariner GW load balancer is not deployed", func() {
			It("should not update the default IPPool's IPIPMode", func() {
				Expect(t.getDefaultIPPoolIPIPMode()).Should(Equal(string(calicoapi.IPIPModeNever)))
			})
		})

		Context("because the Submariner GW load balancer does not have the ROKS annotation", func() {
			BeforeEach(func() {
				t.createSubmarinerGwLBService(map[string]string{})
			})

			It("should not update the default IPPool's IPIPMode", func() {
				Expect(t.getDefaultIPPoolIPIPMode()).Should(Equal(string(calicoapi.IPIPModeNever)))
			})
		})
	})

	When("the platform is ROKS", func() {
		BeforeEach(func() {
			t.createSubmarinerGwLBService(map[string]string{calico.GwLBSvcROKSAnnotation: "foo"})
		})

		It("should update the default IPPool's IPIPMode to Always", func() {
			Expect(t.getDefaultIPPoolIPIPMode()).Should(Equal(string(calicoapi.IPIPModeAlways)))
		})
	})

	Context("on Uninstall", func() {
		BeforeEach(func() {
			t.createSubmarinerGwLBService(map[string]string{calico.GwLBSvcROKSAnnotation: "foo"})

			_, err := t.calicoClient.ProjectcalicoV3().IPPools().Create(context.Background(), &calicoapi.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "pool1",
					Labels: map[string]string{calico.SubmarinerIPPool: "true"},
				},
			}, metav1.CreateOptions{})
			Expect(err).To(Succeed())

			_, err = t.calicoClient.ProjectcalicoV3().IPPools().Create(context.Background(), &calicoapi.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "pool2",
					Labels: map[string]string{calico.SubmarinerIPPool: "true"},
				},
			}, metav1.CreateOptions{})
			Expect(err).To(Succeed())
		})

		JustBeforeEach(func() {
			Expect(t.handler.Uninstall()).To(Succeed())
		})

		It("should delete all IPPools", func() {
			list, err := t.calicoClient.ProjectcalicoV3().IPPools().List(context.Background(), metav1.ListOptions{
				LabelSelector: calico.SubmarinerIPPool + "=true",
			})
			Expect(err).To(Succeed())
			Expect(list.Items).To(BeEmpty())
		})

		It("should reset the default IPPool's IPIPMode", func() {
			Expect(t.getDefaultIPPoolIPIPMode()).Should(Equal(string(calicoapi.IPIPModeNever)))
		})
	})
})
