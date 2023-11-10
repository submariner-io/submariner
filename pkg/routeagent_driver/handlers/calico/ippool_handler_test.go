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
	calicocs "github.com/projectcalico/api/pkg/client/clientset_generated/clientset"
	calicocsfake "github.com/projectcalico/api/pkg/client/clientset_generated/clientset/fake"
	"github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/event/testing"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/calico"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

var _ = Describe("IPPool Handler", func() {
	t := testing.NewControllerSupport()

	var (
		calicoClient *calicocsfake.Clientset
		handler      event.Handler
	)

	BeforeEach(func() {
		calicoClient = calicocsfake.NewSimpleClientset()
		fake.AddDeleteCollectionReactor(&calicoClient.Fake)

		calico.NewClient = func(_ *rest.Config) (calicocs.Interface, error) {
			return calicoClient, nil
		}
	})

	JustBeforeEach(func() {
		handler = calico.NewCalicoIPPoolHandler(nil)
		t.Start(handler)
	})

	When("remote Endpoints are created and deleted", func() {
		It("should create and delete IPPools", func() {
			subnets1 := []string{"192.0.2.0/24", "192.0.3.0/24"}
			remoteEP1 := t.CreateEndpoint(testing.NewEndpoint("remote-cluster1", "host", subnets1...))
			ensureNoIPPools(calicoClient, subnets1...)

			localEP := t.CreateLocalHostEndpoint()
			awaitIPPools(calicoClient, subnets1...)

			// Ensure it handles existing IPPools.
			Expect(handler.RemoteEndpointCreated(remoteEP1)).To(Succeed())

			subnets2 := []string{"192.0.4.0/24"}
			remoteEP2 := t.CreateEndpoint(testing.NewEndpoint("remote-cluster1", "host", subnets2...))
			awaitIPPools(calicoClient, subnets2...)

			t.DeleteEndpoint(remoteEP1.Name)
			awaitNoIPPools(calicoClient, subnets1...)

			t.DeleteEndpoint(localEP.Name)

			t.DeleteEndpoint(remoteEP2.Name)
			ensureIPPools(calicoClient, subnets2...)
		})
	})

	Context("on Uninstall", func() {
		It("should delete all IPPools", func() {
			_, err := calicoClient.ProjectcalicoV3().IPPools().Create(context.Background(), &calicoapi.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "pool1",
					Labels: map[string]string{calico.SubmarinerIPPool: "true"},
				},
			}, metav1.CreateOptions{})
			Expect(err).To(Succeed())

			_, err = calicoClient.ProjectcalicoV3().IPPools().Create(context.Background(), &calicoapi.IPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "pool2",
					Labels: map[string]string{calico.SubmarinerIPPool: "true"},
				},
			}, metav1.CreateOptions{})
			Expect(err).To(Succeed())

			Expect(handler.Uninstall()).To(Succeed())

			list, err := calicoClient.ProjectcalicoV3().IPPools().List(context.Background(), metav1.ListOptions{
				LabelSelector: calico.SubmarinerIPPool + "=true",
			})
			Expect(err).To(Succeed())
			Expect(list.Items).To(BeEmpty())
		})
	})
})

func getIPPoolCIDRs(client calicocs.Interface) []string {
	list, err := client.ProjectcalicoV3().IPPools().List(context.Background(), metav1.ListOptions{
		LabelSelector: calico.SubmarinerIPPool + "=true",
	})
	Expect(err).To(Succeed())

	cidrs := make([]string, len(list.Items))
	for i := range list.Items {
		cidrs[i] = list.Items[i].Spec.CIDR
	}

	return cidrs
}

func ensureNoIPPools(client calicocs.Interface, subnets ...string) {
	Consistently(func() []string {
		return getIPPoolCIDRs(client)
	}).ShouldNot(ContainElements(toAny(subnets)...))
}

func ensureIPPools(client calicocs.Interface, subnets ...string) {
	Consistently(func() []string {
		return getIPPoolCIDRs(client)
	}).Should(ContainElements(toAny(subnets)...))
}

func awaitIPPools(client calicocs.Interface, subnets ...string) {
	Eventually(func() []string {
		return getIPPoolCIDRs(client)
	}).Should(ContainElements(toAny(subnets)...))
}

func awaitNoIPPools(client calicocs.Interface, subnets ...string) {
	Eventually(func() []string {
		return getIPPoolCIDRs(client)
	}).ShouldNot(ContainElements(toAny(subnets)...))
}

func toAny(s []string) []any {
	ia := make([]any, len(s))
	for i := range s {
		ia[i] = s[i]
	}

	return ia
}
