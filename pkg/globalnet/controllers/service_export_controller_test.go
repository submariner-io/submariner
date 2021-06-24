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
	"github.com/submariner-io/admiral/pkg/syncer/test"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
	"github.com/submariner-io/submariner/pkg/ipam"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("ServiceExport controller", func() {
	Describe("Cluster IP Service", testClusterIPService)
	Describe("Headless Service", testHeadlessService)
})

func testClusterIPService() {
	t := newServiceExportControllerTestDriver()

	var service *corev1.Service

	BeforeEach(func() {
		service = newClusterIPService()
		t.createIPTableChain("nat", kubeProxyIPTableChainName)
	})

	When("an existing Service is exported", func() {
		BeforeEach(func() {
			t.createServiceExport(t.createService(service))
		})

		It("should create an appropriate GlobalIngressIP", func() {
			ingressIP := t.awaitGlobalIngressIP(service.Name)
			Expect(ingressIP.Spec.Target).To(Equal(submarinerv1.ClusterIPService))
			Expect(ingressIP.Spec.ServiceRef).ToNot(BeNil())
			Expect(ingressIP.Spec.ServiceRef.Name).To(Equal(service.Name))
		})

		Context("and then unexported", func() {
			It("should delete the GlobalIngressIP", func() {
				t.awaitGlobalIngressIP(service.Name)
				Expect(t.serviceExports.Delete(context.TODO(), service.Name, metav1.DeleteOptions{})).To(Succeed())
				t.awaitNoGlobalIngressIP(service.Name)
			})
		})
	})

	When("a Service is created after being exported", func() {
		BeforeEach(func() {
			t.createServiceExport(service)
		})

		It("should eventually create a GlobalIngressIP", func() {
			t.awaitNoGlobalIngressIP(service.Name)
			t.createService(service)
			t.awaitGlobalIngressIP(service.Name)
		})
	})

	When("an unsupported type Service is exported", func() {
		BeforeEach(func() {
			service.Spec.Type = corev1.ServiceTypeNodePort
			t.createServiceExport(t.createService(service))
		})

		It("should not create a GlobalIngressIP", func() {
			t.awaitNoGlobalIngressIP(service.Name)
		})
	})
}

func testHeadlessService() {
	t := newServiceExportControllerTestDriver()

	var service *corev1.Service
	var backendPod *corev1.Pod

	BeforeEach(func() {
		service = newHeadlessService()
		backendPod = newHeadlessServicePod(service.Name)
		t.createServiceExport(t.createService(service))
	})

	When("a backend Pod for an exported Service is created", func() {
		BeforeEach(func() {
			t.createPod(backendPod)
		})

		It("should create an appropriate GlobalIngressIP", func() {
			t.awaitHeadlessGlobalIngressIP(service.Name, backendPod.Name)
		})

		Context("and then deleted", func() {
			It("should delete the GlobalIngressIP", func() {
				ingressIP := t.awaitHeadlessGlobalIngressIP(service.Name, backendPod.Name)
				t.deletePod(backendPod)
				t.awaitNoGlobalIngressIP(ingressIP.Name)
			})
		})
	})

	When("a backend Pod for an exported Service isn't initially running", func() {
		BeforeEach(func() {
			backendPod.Status.Phase = corev1.PodPending
			t.createPod(backendPod)
		})

		It("should eventually create a GlobalIngressIP", func() {
			t.awaitNoGlobalIngressIPs()

			backendPod.Status.Phase = corev1.PodRunning
			test.UpdateResource(t.pods.Namespace(namespace), backendPod)
			t.awaitHeadlessGlobalIngressIP(service.Name, backendPod.Name)
		})
	})

	When("a backend Pod for an exported Service doesn't initially have an IP", func() {
		BeforeEach(func() {
			backendPod.Status.PodIP = ""
			t.createPod(backendPod)
		})

		It("should eventually create a GlobalIngressIP", func() {
			t.awaitNoGlobalIngressIPs()

			backendPod.Status.PodIP = "154.67.82.2"
			test.UpdateResource(t.pods.Namespace(namespace), backendPod)
			t.awaitHeadlessGlobalIngressIP(service.Name, backendPod.Name)
		})
	})

	When("a Service is unexported", func() {
		var backendPod2 *corev1.Pod
		var otherPod *corev1.Pod

		BeforeEach(func() {
			t.createPod(backendPod)
			backendPod2 = t.createPod(newHeadlessServicePod(service.Name))
			otherPod = t.createPod(newPod(namespace))
		})

		It("should delete the GlobalIngressIP objects associated to the backend Pods", func() {
			ingressIP1 := t.awaitHeadlessGlobalIngressIP(service.Name, backendPod.Name)
			ingressIP2 := t.awaitHeadlessGlobalIngressIP(service.Name, backendPod2.Name)

			Expect(t.serviceExports.Delete(context.TODO(), service.Name, metav1.DeleteOptions{})).To(Succeed())
			t.awaitNoGlobalIngressIP(ingressIP1.Name)
			t.awaitNoGlobalIngressIP(ingressIP2.Name)

			test.GetResource(t.pods.Namespace(namespace), otherPod)
		})
	})

	When("a Pod not associated to an exported Service is created", func() {
		BeforeEach(func() {
			t.createPod(newPod(namespace))
		})

		It("should not create a GlobalIngressIP", func() {
			t.awaitNoGlobalIngressIPs()
		})
	})
}

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
