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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var _ = Describe("Service controller", func() {
	t := newServiceControllerTestDriver()

	var service *corev1.Service

	When("an exported cluster IP Service is deleted and subsequently re-created", func() {
		BeforeEach(func() {
			service = newClusterIPService()
			t.createServiceExport(t.createService(service))
		})

		JustBeforeEach(func() {
			t.awaitGlobalIngressIP(service.Name)
		})

		It("should delete the GlobalIngressIP and then re-create it", func() {
			By("Deleting the service")

			Expect(t.services.Delete(context.TODO(), service.Name, metav1.DeleteOptions{})).To(Succeed())
			t.awaitNoGlobalIngressIP(service.Name)

			By("Re-creating the service")

			t.createService(service)
			t.awaitGlobalIngressIP(service.Name)
		})
	})

	When("an exported headless Service is deleted and subsequently re-created", func() {
		var backendPod *corev1.Pod

		BeforeEach(func() {
			service = newHeadlessService()
			backendPod = newHeadlessServicePod(service.Name)
			t.createServiceExport(t.createService(service))
		})

		JustBeforeEach(func() {
			t.createPod(backendPod)
			t.awaitHeadlessGlobalIngressIP(service.Name, backendPod.Name)
		})

		It("should delete the GlobalIngressIP objects associated with the backend Pods and then re-create them", func() {
			By("Deleting the service")

			Expect(t.services.Delete(context.TODO(), service.Name, metav1.DeleteOptions{})).To(Succeed())

			Eventually(func() []unstructured.Unstructured {
				list, _ := t.globalIngressIPs.List(context.TODO(), metav1.ListOptions{})
				return list.Items
			}, 5).Should(BeEmpty())

			By("Re-creating the service")

			t.createService(service)
			t.awaitHeadlessGlobalIngressIP(service.Name, backendPod.Name)
		})
	})

	When("a GlobalIngressIP is stale on startup due to a missed delete event", func() {
		Context("for a cluster IP Service", func() {
			BeforeEach(func() {
				service = newClusterIPService()
				t.createServiceExport(service)
				t.createGlobalIngressIP(&submarinerv1.GlobalIngressIP{
					ObjectMeta: metav1.ObjectMeta{
						Name: service.Name,
					},
					Spec: submarinerv1.GlobalIngressIPSpec{
						Target:     submarinerv1.ClusterIPService,
						ServiceRef: &corev1.LocalObjectReference{Name: service.Name},
					},
				})
			})

			It("should delete the GlobalIngressIP on reconciliation", func() {
				t.awaitNoGlobalIngressIP(service.Name)
			})
		})

		Context("for a headless Service", func() {
			BeforeEach(func() {
				t.createServiceExport(newHeadlessService())
				t.createGlobalIngressIP(&submarinerv1.GlobalIngressIP{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pod-one",
						Labels: map[string]string{
							controllers.ServiceRefLabel: service.Name,
						},
					},
					Spec: submarinerv1.GlobalIngressIPSpec{
						Target:     submarinerv1.HeadlessServicePod,
						ServiceRef: &corev1.LocalObjectReference{Name: service.Name},
					},
				})
			})

			It("should delete the GlobalIngressIP on reconciliation", func() {
				t.awaitNoGlobalIngressIP("pod-one")
			})
		})
	})
})

type serviceControllerTestDriver struct {
	*testDriverBase
}

func newServiceControllerTestDriver() *serviceControllerTestDriver {
	t := &serviceControllerTestDriver{}

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

func (t *serviceControllerTestDriver) start() {
	seTestDriver := &serviceExportControllerTestDriver{}
	seTestDriver.testDriverBase = t.testDriverBase
	config, podControllers, syncer := seTestDriver.start()

	var err error

	t.controller, err = controllers.NewServiceController(config, podControllers, syncer)

	Expect(err).To(Succeed())
	Expect(t.controller.Start()).To(Succeed())
}
