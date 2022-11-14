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

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var _ = Describe("Service controller", func() {
	t := newServiceControllerTestDriver()

	When("an exported cluster IP Service is deleted", func() {
		BeforeEach(func() {
			t.createServiceExport(t.createService(newClusterIPService()))
			t.createGlobalIngressIP(&submarinerv1.GlobalIngressIP{
				ObjectMeta: metav1.ObjectMeta{
					Name: serviceName,
				},
			})
		})

		It("should delete the GlobalIngressIP", func() {
			Expect(t.services.Delete(context.TODO(), serviceName, metav1.DeleteOptions{})).To(Succeed())
			t.awaitNoGlobalIngressIP(serviceName)
		})
	})

	When("an exported headless Service is deleted", func() {
		BeforeEach(func() {
			t.createServiceExport(t.createService(newHeadlessService()))

			t.createGlobalIngressIP(&submarinerv1.GlobalIngressIP{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pod-1",
					Labels: map[string]string{
						controllers.ServiceRefLabel: serviceName,
					},
				},
			})

			t.createGlobalIngressIP(&submarinerv1.GlobalIngressIP{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pod-2",
					Labels: map[string]string{
						controllers.ServiceRefLabel: serviceName,
					},
				},
			})
		})

		It("should delete the GlobalIngressIP objects associated with the backend Pods", func() {
			Expect(t.services.Delete(context.TODO(), serviceName, metav1.DeleteOptions{})).To(Succeed())

			Eventually(func() []unstructured.Unstructured {
				list, _ := t.globalIngressIPs.List(context.TODO(), metav1.ListOptions{})
				return list.Items
			}, 5).Should(BeEmpty())
		})
	})

	When("a GlobalIngressIP is stale on startup due to a missed delete event", func() {
		Context("for a cluster IP Service", func() {
			BeforeEach(func() {
				t.createServiceExport(newClusterIPService())
				t.createGlobalIngressIP(&submarinerv1.GlobalIngressIP{
					ObjectMeta: metav1.ObjectMeta{
						Name: serviceName,
					},
					Spec: submarinerv1.GlobalIngressIPSpec{
						Target:     submarinerv1.ClusterIPService,
						ServiceRef: &corev1.LocalObjectReference{Name: serviceName},
					},
				})
			})

			It("should delete the GlobalIngressIP on reconciliation", func() {
				t.awaitNoGlobalIngressIP(serviceName)
			})
		})

		Context("for a headless Service", func() {
			BeforeEach(func() {
				t.createServiceExport(newHeadlessService())
				t.createGlobalIngressIP(&submarinerv1.GlobalIngressIP{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pod-one",
						Labels: map[string]string{
							controllers.ServiceRefLabel: serviceName,
						},
					},
					Spec: submarinerv1.GlobalIngressIPSpec{
						Target:     submarinerv1.HeadlessServicePod,
						ServiceRef: &corev1.LocalObjectReference{Name: serviceName},
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
	var err error

	config := &syncer.ResourceSyncerConfig{
		SourceClient: t.dynClient,
		RestMapper:   t.restMapper,
		Scheme:       t.scheme,
	}

	podControllers, err := controllers.NewIngressPodControllers(config)
	Expect(err).To(Succeed())

	t.controller, err = controllers.NewServiceController(config, podControllers)

	Expect(err).To(Succeed())
	Expect(t.controller.Start()).To(Succeed())
}
