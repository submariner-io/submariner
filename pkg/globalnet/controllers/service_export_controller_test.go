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
	"github.com/submariner-io/submariner/pkg/ipam"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("ServiceExport controller", func() {
	t := newServiceExportControllerTestDriver()

	When("an existing cluster IP Service is exported", func() {
		BeforeEach(func() {
			t.createServiceExport(t.createService(newClusterIPService()))
		})

		It("should create an appropriate GlobalIngressIP", func() {
			ingressIP := t.awaitGlobalIngressIP(serviceName)
			Expect(ingressIP.Spec.Target).To(Equal(submarinerv1.ClusterIPService))
			Expect(ingressIP.Spec.ServiceRef).ToNot(BeNil())
			Expect(ingressIP.Spec.ServiceRef.Name).To(Equal(serviceName))
		})

		Context("and then unexported", func() {
			It("should delete the GlobalIngressIP", func() {
				t.awaitGlobalIngressIP(serviceName)
				Expect(t.serviceExports.Delete(context.TODO(), serviceName, metav1.DeleteOptions{})).To(Succeed())
				t.awaitNoGlobalIngressIP(serviceName)
			})
		})
	})

	When("an existing headless Service is exported", func() {
		BeforeEach(func() {
			t.createServiceExport(t.createService(newHeadlessService(serviceName)))
		})

		It("should create an appropriate GlobalIngressIP", func() {
			// TODO
		})
	})

	When("a Service is created after being exported", func() {
		var service *corev1.Service

		BeforeEach(func() {
			service = newClusterIPService()
			t.createServiceExport(service)
		})

		It("should eventually create a GlobalIngressIP", func() {
			t.awaitNoGlobalIngressIP(serviceName)
			t.createService(service)
			t.awaitGlobalIngressIP(serviceName)
		})
	})

	When("an unsupported type Service is exported", func() {
		BeforeEach(func() {
			t.createServiceExport(t.createService(&corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name: serviceName,
				},
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeNodePort,
				},
			}))
		})

		It("should not create a GlobalIngressIP", func() {
			t.awaitNoGlobalIngressIP(serviceName)
		})
	})
})

type serviceExportControllerTestDriver struct {
	*testDriverBase
}

func newServiceExportControllerTestDriver() *serviceExportControllerTestDriver {
	t := &serviceExportControllerTestDriver{}

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

func (t *serviceExportControllerTestDriver) start() {
	var err error

	t.pool, err = ipam.NewIPPool(t.globalCIDR)
	Expect(err).To(Succeed())

	t.controller, err = controllers.NewServiceExportController(syncer.ResourceSyncerConfig{
		SourceClient: t.dynClient,
		RestMapper:   t.restMapper,
		Scheme:       t.scheme,
	})

	Expect(err).To(Succeed())
	Expect(t.controller.Start()).To(Succeed())
}
