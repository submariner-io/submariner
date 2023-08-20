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
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
	"github.com/submariner-io/submariner/pkg/ipam"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var _ = Describe("ServiceExport controller", func() {
	Describe("Cluster IP Service", testClusterIPService)
	Describe("Headless Service", testHeadlessService)
	Describe("Service without selector", testServiceWithoutSelector)
	Describe("Headless Service without selector", testHeadlessServiceWithoutSelector)
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
			t.ensureNoGlobalIngressIP(service.Name)
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
			t.ensureNoGlobalIngressIP(service.Name)
		})
	})

	When("a GlobalIngressIP is stale on startup due to a missed ServiceExport delete event", func() {
		BeforeEach(func() {
			t.createServiceExport(t.createService(service))
		})

		It("should delete the GlobalIngressIP on reconciliation", func() {
			t.awaitGlobalIngressIP(serviceName)

			t.controller.Stop()
			time.Sleep(500 * time.Millisecond)
			Expect(t.serviceExports.Delete(context.TODO(), serviceName, metav1.DeleteOptions{})).To(Succeed())

			t.start()
			t.awaitNoGlobalIngressIP(serviceName)
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
	})

	JustBeforeEach(func() {
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

	When("a backend Pod for an exported Service isn't running", func() {
		BeforeEach(func() {
			backendPod.Status.Phase = corev1.PodPending
			t.createPod(backendPod)
		})

		It("should eventually create a GlobalIngressIP after the Pod transitions to running", func() {
			t.ensureNoGlobalIngressIPs()

			backendPod.Status.Phase = corev1.PodRunning
			test.UpdateResource(t.pods.Namespace(namespace), backendPod)
			t.awaitHeadlessGlobalIngressIP(service.Name, backendPod.Name)
		})

		Context("and PublishNotReadyAddresses is set to true on the Service", func() {
			BeforeEach(func() {
				service.Spec.PublishNotReadyAddresses = true
			})

			It("should create a GlobalIngressIP", func() {
				t.awaitHeadlessGlobalIngressIP(service.Name, backendPod.Name)
			})
		})
	})

	When("a backend Pod for an exported Service doesn't initially have an IP", func() {
		BeforeEach(func() {
			backendPod.Status.PodIP = ""
			t.createPod(backendPod)
		})

		It("should eventually create a GlobalIngressIP", func() {
			t.ensureNoGlobalIngressIPs()

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

		It("should delete the GlobalIngressIP objects associated with the backend Pods", func() {
			ingressIP1 := t.awaitHeadlessGlobalIngressIP(service.Name, backendPod.Name)
			ingressIP2 := t.awaitHeadlessGlobalIngressIP(service.Name, backendPod2.Name)

			Expect(t.serviceExports.Delete(context.TODO(), service.Name, metav1.DeleteOptions{})).To(Succeed())
			t.awaitNoGlobalIngressIP(ingressIP1.Name)
			t.awaitNoGlobalIngressIP(ingressIP2.Name)

			test.GetResource(t.pods.Namespace(namespace), otherPod)

			// Ensure GlobalIngressIPs are no longer created for the service.
			t.createPod(newHeadlessServicePod(service.Name))
			t.ensureNoGlobalIngressIPs()
		})
	})

	When("a Pod not associated to an exported Service is created", func() {
		BeforeEach(func() {
			t.createPod(newPod(namespace))
		})

		It("should not create a GlobalIngressIP", func() {
			t.ensureNoGlobalIngressIPs()
		})
	})

	When("backend Pod GlobalIngressIPs are stale on startup due to a missed ServiceExport delete event", func() {
		var backendPod2 *corev1.Pod

		BeforeEach(func() {
			t.createPod(backendPod)
			backendPod2 = t.createPod(newHeadlessServicePod(service.Name))
		})

		It("should delete the GlobalIngressIPs on reconciliation", func() {
			t.awaitHeadlessGlobalIngressIP(service.Name, backendPod.Name)
			t.awaitHeadlessGlobalIngressIP(service.Name, backendPod2.Name)

			t.controller.Stop()
			time.Sleep(500 * time.Millisecond)
			Expect(t.serviceExports.Delete(context.TODO(), serviceName, metav1.DeleteOptions{})).To(Succeed())

			t.start()

			Eventually(func() []unstructured.Unstructured {
				list, _ := t.globalIngressIPs.List(context.TODO(), metav1.ListOptions{})
				return list.Items
			}, time.Second*3).Should(BeEmpty())
		})
	})

	When("a backend Pod GlobalIngressIP is stale on startup due to a missed Pod delete event", func() {
		BeforeEach(func() {
			t.createPod(backendPod)
		})

		It("should delete the GlobalIngressIPs on reconciliation", func() {
			t.awaitHeadlessGlobalIngressIP(service.Name, backendPod.Name)

			t.controller.Stop()
			time.Sleep(500 * time.Millisecond)
			Expect(t.pods.Namespace(backendPod.Namespace).Delete(context.TODO(), backendPod.Name, metav1.DeleteOptions{})).To(Succeed())

			t.start()
			Eventually(func() []unstructured.Unstructured {
				list, _ := t.globalIngressIPs.List(context.TODO(), metav1.ListOptions{})
				return list.Items
			}, time.Second*3).Should(BeEmpty())
		})
	})
}

func testServiceWithoutSelector() {
	t := newServiceExportControllerTestDriver()

	var service *corev1.Service
	var endpoints *corev1.Endpoints

	BeforeEach(func() {
		service = newServiceWithoutSelector()
		endpoints = newDefaultEndpoints(service.Name)
	})

	When("Endpoints resource is created before service is exported", func() {
		BeforeEach(func() {
			t.createService(service)
			endpoints = t.createEndpoints(endpoints)
			t.awaitEndpoints(endpoints.Name)
			t.createServiceExport(service)
		})

		It("should create an appropriate cloned Endpoints resource", func() {
			t.awaitEndpoints(controllers.GetInternalSvcName(endpoints.Name))
		})

		Context("and then original Endpoints resource is deleted", func() {
			It("should delete the cloned endpoints", func() {
				t.awaitEndpoints(controllers.GetInternalSvcName(endpoints.Name))
				t.deleteEndpoints(endpoints)
				t.awaitNoEndpoints(controllers.GetInternalSvcName(endpoints.Name))
			})
		})

		Context("and then original Endpoints resource is updated", func() {
			It("should update the cloned endpoints", func() {
				oldIP := "172.45.5.6" // defined in newEndpoints()
				newIP := "172.45.5.7"

				clonedEp := t.awaitEndpoints(controllers.GetInternalSvcName(endpoints.Name))

				// Confirm that both endpoints and clonedEP have oldIP
				t.awaitEndpointsHasIP(endpoints.Name, oldIP)
				t.awaitEndpointsHasIP(clonedEp.Name, oldIP)

				// Update endpoints to have newIP
				updatedEp := newEndpoints(endpoints.Name, newIP, endpoints.Labels)
				updatedEp = t.updateEndpoints(updatedEp)

				// Confirm that both endpoints and clonedEP have newIP
				t.awaitEndpointsHasIP(updatedEp.Name, newIP)
				t.awaitEndpointsHasIP(clonedEp.Name, newIP)
			})
		})
	})

	When("Endpoints resource is created after service is exported", func() {
		BeforeEach(func() {
			t.createService(service)
			t.createServiceExport(service)
			endpoints = t.createEndpoints(endpoints)
			t.awaitEndpoints(endpoints.Name)
		})

		It("should create an appropriate cloned Endpoints resource", func() {
			t.awaitEndpoints(controllers.GetInternalSvcName(endpoints.Name))
		})

		Context("and then original endpoints is deleted", func() {
			It("should delete the cloned endpoints", func() {
				t.awaitEndpoints(controllers.GetInternalSvcName(endpoints.Name))
				t.deleteEndpoints(endpoints)
				t.awaitNoEndpoints(controllers.GetInternalSvcName(endpoints.Name))
			})
		})

		Context("and then original Endpoints resource is updated", func() {
			It("should update the cloned Endpoints resource", func() {
				oldIP := "172.45.5.6" // defined in newEndpoints()
				newIP := "172.45.5.7"

				clonedEp := t.awaitEndpoints(controllers.GetInternalSvcName(endpoints.Name))

				// Confirm that both endpoints and clonedEP have oldIP
				t.awaitEndpointsHasIP(endpoints.Name, oldIP)
				t.awaitEndpointsHasIP(clonedEp.Name, oldIP)

				// Update endpoints to have newIP
				updatedEp := newEndpoints(endpoints.Name, newIP, endpoints.Labels)
				updatedEp = t.updateEndpoints(updatedEp)

				// Confirm that both endpoints and clonedEP have newIP
				t.awaitEndpointsHasIP(updatedEp.Name, newIP)
				t.awaitEndpointsHasIP(clonedEp.Name, newIP)
			})
		})
	})

	When("cloned Endpoints resource is created", func() {
		JustBeforeEach(func() {
			t.createService(service)
			t.createServiceExport(service)
			endpoints = t.createEndpoints(endpoints)
			t.awaitEndpoints(endpoints.Name)
			t.awaitEndpoints(controllers.GetInternalSvcName(endpoints.Name))
		})

		Context("and then controller is stopped", func() {
			It("should keep the cloned Endpoints resource on controller restart if the original still exists", func() {
				t.controller.Stop()

				time.Sleep(50 * time.Millisecond)
				t.awaitEndpoints(endpoints.Name)
				t.awaitEndpoints(controllers.GetInternalSvcName(endpoints.Name))

				// Restart cotroller
				t.start()

				time.Sleep(50 * time.Millisecond)
				t.awaitEndpoints(controllers.GetInternalSvcName(endpoints.Name))
			})

			It("should delete the cloned Endpoints resource on controller restart if the original has been deleted", func() {
				t.controller.Stop()

				time.Sleep(50 * time.Millisecond)
				t.awaitEndpoints(endpoints.Name)
				t.awaitEndpoints(controllers.GetInternalSvcName(endpoints.Name))

				// Delete original endpoints before restart controller
				t.deleteEndpoints(endpoints)
				t.awaitNoEndpoints(endpoints.Name)

				t.start()

				t.ensureNoEndpoints(controllers.GetInternalSvcName(endpoints.Name))
			})
		})
	})
}

func testHeadlessServiceWithoutSelector() {
	t := newServiceExportControllerTestDriver()

	var service *corev1.Service
	var endpoints *corev1.Endpoints

	BeforeEach(func() {
		service = newHeadlessServiceWithoutSelector()
		endpoints = newHeadlessServiceEndpoints(service.Name)
		t.createServiceExport(t.createService(service))
	})

	When("an endpoint for an exported Service is created", func() {
		BeforeEach(func() {
			t.createEndpoints(endpoints)
		})

		It("should create an appropriate GlobalIngressIP", func() {
			t.awaitHeadlessGlobalIngressIPForEP(service.Name, endpoints.Name)
		})

		Context("and then deleted", func() {
			It("should delete the GlobalIngressIP", func() {
				ingressIP := t.awaitHeadlessGlobalIngressIPForEP(service.Name, endpoints.Name)
				t.deleteEndpoints(endpoints)
				t.awaitNoGlobalIngressIP(ingressIP.Name)
			})
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

func (t *serviceExportControllerTestDriver) start() (*syncer.ResourceSyncerConfig, *controllers.IngressPodControllers, syncer.Interface) {
	var err error

	t.pool, err = ipam.NewIPPool(t.globalCIDR)
	Expect(err).To(Succeed())

	config := &syncer.ResourceSyncerConfig{
		SourceClient: t.dynClient,
		RestMapper:   t.restMapper,
		Scheme:       t.scheme,
	}

	podControllers, err := controllers.NewIngressPodControllers(config)
	Expect(err).To(Succeed())

	endpointsControllers, err := controllers.NewServiceExportEndpointsControllers(config)
	Expect(err).To(Succeed())

	ingressEndpointsControllers, err := controllers.NewIngressEndpointsControllers(config)
	Expect(err).To(Succeed())

	controller, err := controllers.NewServiceExportController(config, podControllers, endpointsControllers, ingressEndpointsControllers)
	t.controller = controller

	Expect(err).To(Succeed())
	Expect(t.controller.Start()).To(Succeed())

	return config, podControllers, controller.GetSyncer()
}
