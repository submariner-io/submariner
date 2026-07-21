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
	"github.com/submariner-io/admiral/pkg/ipam"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	testutil "github.com/submariner-io/admiral/pkg/test"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
	"github.com/submariner-io/submariner/pkg/globalnet/metrics"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	mcsv1b1 "sigs.k8s.io/mcs-api/pkg/apis/v1beta1"
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

		t.createPFilterChain(packetfilter.TableTypeNAT, kubeProxyIPTableChainName)
	})

	When("an existing Service is exported", func() {
		BeforeEach(func(ctx context.Context) {
			t.createServiceExport(ctx, t.createService(ctx, service))
		})

		It("should create an appropriate GlobalIngressIP", func(ctx context.Context) {
			ingressIP := t.awaitGlobalIngressIP(ctx, service.Name)
			Expect(ingressIP.Spec.Target).To(Equal(submarinerv1.ClusterIPService))
			Expect(ingressIP.Spec.ServiceRef).ToNot(BeNil())
			Expect(ingressIP.Spec.ServiceRef.Name).To(Equal(service.Name))
		})

		Context("and then unexported", func() {
			It("should delete the GlobalIngressIP", func(ctx context.Context) {
				t.awaitGlobalIngressIP(ctx, service.Name)
				Expect(t.serviceExports.Delete(ctx, service.Name, metav1.DeleteOptions{})).To(Succeed())
				t.awaitNoGlobalIngressIP(ctx, service.Name)
			})
		})
	})

	When("a Service is created after being exported", func() {
		BeforeEach(func(ctx context.Context) {
			t.createServiceExport(ctx, service)
		})

		It("should eventually create a GlobalIngressIP", func(ctx context.Context) {
			t.ensureNoGlobalIngressIP(ctx, service.Name)
			t.createService(ctx, service)
			t.awaitGlobalIngressIP(ctx, service.Name)
		})
	})

	When("an unsupported type Service is exported", func() {
		BeforeEach(func(ctx context.Context) {
			service.Spec.Type = corev1.ServiceTypeNodePort
			t.createServiceExport(ctx, t.createService(ctx, service))
		})

		It("should not create a GlobalIngressIP", func(ctx context.Context) {
			t.ensureNoGlobalIngressIP(ctx, service.Name)
		})
	})

	When("a GlobalIngressIP is stale on startup due to a missed ServiceExport delete event", func() {
		BeforeEach(func(ctx context.Context) {
			t.createServiceExport(ctx, t.createService(ctx, service))
		})

		It("should delete the GlobalIngressIP on reconciliation", func(ctx context.Context) {
			t.awaitGlobalIngressIP(ctx, serviceName)

			t.controller.Stop(ctx)
			time.Sleep(500 * time.Millisecond)
			Expect(t.serviceExports.Delete(ctx, serviceName, metav1.DeleteOptions{})).To(Succeed())

			t.start(ctx)
			t.awaitNoGlobalIngressIP(ctx, serviceName)
		})
	})

	When("a dual-stack Service is exported", func() {
		BeforeEach(func(ctx context.Context) {
			service.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv6Protocol, corev1.IPv4Protocol}
			service.Spec.ClusterIPs = []string{ipv6IP, service.Spec.ClusterIP}

			t.createServiceExport(ctx, t.createService(ctx, service))
		})

		It("should create a GlobalIngressIP", func(ctx context.Context) {
			t.awaitGlobalIngressIP(ctx, service.Name)
		})
	})

	When("an IPv6 Service is exported", func() {
		BeforeEach(func(ctx context.Context) {
			service.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv6Protocol}
			service.Spec.ClusterIP = ipv6IP
			service.Spec.ClusterIPs = []string{ipv6IP}

			t.createServiceExport(ctx, t.createService(ctx, service))
		})

		It("should not create a GlobalIngressIP", func(ctx context.Context) {
			t.ensureNoGlobalIngressIP(ctx, service.Name)
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

	JustBeforeEach(func(ctx context.Context) {
		t.createServiceExport(ctx, t.createService(ctx, service))
	})

	When("a backend Pod for an exported Service is created", func() {
		BeforeEach(func(ctx context.Context) {
			t.createPod(ctx, backendPod)
		})

		It("should create an appropriate GlobalIngressIP", func(ctx context.Context) {
			t.awaitHeadlessGlobalIngressIP(ctx, service.Name, backendPod.Name)
		})

		Context("and then deleted", func() {
			It("should delete the GlobalIngressIP", func(ctx context.Context) {
				ingressIP := t.awaitHeadlessGlobalIngressIP(ctx, service.Name, backendPod.Name)
				t.deletePod(ctx, backendPod)
				t.awaitNoGlobalIngressIP(ctx, ingressIP.Name)
			})
		})
	})

	When("a backend Pod for an exported Service isn't running", func() {
		BeforeEach(func(ctx context.Context) {
			backendPod.Status.Phase = corev1.PodPending
			t.createPod(ctx, backendPod)
		})

		It("should eventually create a GlobalIngressIP after the Pod transitions to running", func(ctx context.Context) {
			t.ensureNoGlobalIngressIPs(ctx)

			backendPod.Status.Phase = corev1.PodRunning
			test.UpdateResource(ctx, t.pods.Namespace(namespace), backendPod)
			t.awaitHeadlessGlobalIngressIP(ctx, service.Name, backendPod.Name)
		})

		Context("and PublishNotReadyAddresses is set to true on the Service", func() {
			BeforeEach(func() {
				service.Spec.PublishNotReadyAddresses = true
			})

			It("should create a GlobalIngressIP", func(ctx context.Context) {
				t.awaitHeadlessGlobalIngressIP(ctx, service.Name, backendPod.Name)
			})
		})
	})

	When("a backend Pod for an exported Service doesn't initially have an IP", func() {
		BeforeEach(func(ctx context.Context) {
			backendPod.Status.PodIP = ""
			t.createPod(ctx, backendPod)
		})

		It("should eventually create a GlobalIngressIP", func(ctx context.Context) {
			t.ensureNoGlobalIngressIPs(ctx)

			backendPod.Status.PodIP = "154.67.82.2"
			test.UpdateResource(ctx, t.pods.Namespace(namespace), backendPod)
			t.awaitHeadlessGlobalIngressIP(ctx, service.Name, backendPod.Name)
		})
	})

	When("a Service is unexported", func() {
		var backendPod2 *corev1.Pod
		var otherPod *corev1.Pod

		BeforeEach(func(ctx context.Context) {
			t.createPod(ctx, backendPod)
			backendPod2 = t.createPod(ctx, newHeadlessServicePod(service.Name))
			otherPod = t.createPod(ctx, newPod(namespace))
		})

		It("should delete the GlobalIngressIP objects associated with the backend Pods", func(ctx context.Context) {
			ingressIP1 := t.awaitHeadlessGlobalIngressIP(ctx, service.Name, backendPod.Name)
			ingressIP2 := t.awaitHeadlessGlobalIngressIP(ctx, service.Name, backendPod2.Name)

			Expect(t.serviceExports.Delete(ctx, service.Name, metav1.DeleteOptions{})).To(Succeed())
			t.awaitNoGlobalIngressIP(ctx, ingressIP1.Name)
			t.awaitNoGlobalIngressIP(ctx, ingressIP2.Name)

			test.GetResource(ctx, t.pods.Namespace(namespace), otherPod)

			// Ensure GlobalIngressIPs are no longer created for the service.
			t.createPod(ctx, newHeadlessServicePod(service.Name))
			t.ensureNoGlobalIngressIPs(ctx)
		})
	})

	When("a Pod not associated to an exported Service is created", func() {
		BeforeEach(func(ctx context.Context) {
			t.createPod(ctx, newPod(namespace))
		})

		It("should not create a GlobalIngressIP", func(ctx context.Context) {
			t.ensureNoGlobalIngressIPs(ctx)
		})
	})

	When("backend Pod GlobalIngressIPs are stale on startup due to a missed ServiceExport delete event", func() {
		var backendPod2 *corev1.Pod

		BeforeEach(func(ctx context.Context) {
			t.createPod(ctx, backendPod)
			backendPod2 = t.createPod(ctx, newHeadlessServicePod(service.Name))
		})

		It("should delete the GlobalIngressIPs on reconciliation", func(ctx context.Context) {
			t.awaitHeadlessGlobalIngressIP(ctx, service.Name, backendPod.Name)
			t.awaitHeadlessGlobalIngressIP(ctx, service.Name, backendPod2.Name)

			t.controller.Stop(ctx)
			time.Sleep(500 * time.Millisecond)
			Expect(t.serviceExports.Delete(ctx, serviceName, metav1.DeleteOptions{})).To(Succeed())

			t.start(ctx)

			Eventually(ctx, func(g Gomega, ctx context.Context) {
				list, err := t.globalIngressIPs.List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(list.Items).To(BeEmpty())
			}).Within(time.Second * 3).Should(Succeed())
		})
	})

	When("a backend Pod GlobalIngressIP is stale on startup due to a missed Pod delete event", func() {
		BeforeEach(func(ctx context.Context) {
			t.createPod(ctx, backendPod)
		})

		It("should delete the GlobalIngressIPs on reconciliation", func(ctx context.Context) {
			t.awaitHeadlessGlobalIngressIP(ctx, service.Name, backendPod.Name)

			t.controller.Stop(ctx)
			time.Sleep(500 * time.Millisecond)
			Expect(t.pods.Namespace(backendPod.Namespace).Delete(ctx, backendPod.Name, metav1.DeleteOptions{})).To(Succeed())

			t.start(ctx)
			Eventually(ctx, func(g Gomega, ctx context.Context) {
				list, err := t.globalIngressIPs.List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(list.Items).To(BeEmpty())
			}).Within(time.Second * 3).Should(Succeed())
		})
	})

	When("the same headless Service name is exported from another namespace", func() {
		const otherNamespace = "other-ns"

		var otherService *corev1.Service
		var otherPod *corev1.Pod

		BeforeEach(func(ctx context.Context) {
			t.createPod(ctx, backendPod)

			otherService = newHeadlessService()
			otherService.Namespace = otherNamespace

			otherPod = newHeadlessServicePod(otherService.Name)
			otherPod.Namespace = otherNamespace
			otherPod.Status.PodIP = "172.45.4.4"
		})

		It("should retain GlobalIngressIPs for both namespaces", func(ctx context.Context) {
			localIngressIP := t.awaitHeadlessGlobalIngressIP(ctx, service.Name, backendPod.Name)

			gvrService := *test.GetGroupVersionResourceFor(t.restMapper, &corev1.Service{})
			gvrServiceExport := *test.GetGroupVersionResourceFor(t.restMapper, &mcsv1b1.ServiceExport{})
			gvrGlobalIngressIP := *test.GetGroupVersionResourceFor(t.restMapper, &submarinerv1.GlobalIngressIP{})

			test.CreateResource(ctx, t.dynClient.Resource(gvrService).Namespace(otherNamespace), otherService)
			t.createPod(ctx, otherPod)
			test.CreateResource(ctx, t.dynClient.Resource(gvrServiceExport).Namespace(otherNamespace), &mcsv1b1.ServiceExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      otherService.Name,
					Namespace: otherNamespace,
				},
			})

			var otherIngressIP *submarinerv1.GlobalIngressIP

			Eventually(ctx, func(g Gomega, ctx context.Context) {
				list, err := t.dynClient.Resource(gvrGlobalIngressIP).Namespace(otherNamespace).List(ctx, metav1.ListOptions{})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(list.Items).NotTo(BeEmpty())

				gip := &submarinerv1.GlobalIngressIP{}
				g.Expect(runtime.DefaultUnstructuredConverter.FromUnstructured(list.Items[0].Object, gip)).To(Succeed())
				otherIngressIP = gip
			}).Within(5 * time.Second).Should(Succeed())

			Expect(otherIngressIP.Spec.ServiceRef.Name).To(Equal(otherService.Name))
			Expect(otherIngressIP.Spec.PodRef.Name).To(Equal(otherPod.Name))

			Consistently(ctx, func(ctx context.Context) error {
				_, err := t.globalIngressIPs.Get(ctx, localIngressIP.Name, metav1.GetOptions{})

				return err
			}).Within(2 * time.Second).Should(Succeed())

			Consistently(ctx, func(ctx context.Context) error {
				_, err := t.dynClient.Resource(gvrGlobalIngressIP).Namespace(otherNamespace).
					Get(ctx, otherIngressIP.Name, metav1.GetOptions{})

				return err
			}).Within(2 * time.Second).Should(Succeed())
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
		BeforeEach(func(ctx context.Context) {
			t.createService(ctx, service)
			endpoints = t.createEndpoints(ctx, endpoints)
			t.awaitEndpoints(ctx, endpoints.Name)
			t.createServiceExport(ctx, service)
		})

		It("should create an appropriate cloned Endpoints resource", func(ctx context.Context) {
			t.awaitEndpoints(ctx, controllers.GetInternalSvcName(endpoints.Name))
		})

		Context("and then original Endpoints resource is deleted", func() {
			It("should delete the cloned endpoints", func(ctx context.Context) {
				t.awaitEndpoints(ctx, controllers.GetInternalSvcName(endpoints.Name))
				t.deleteEndpoints(ctx, endpoints)
				t.awaitNoEndpoints(ctx, controllers.GetInternalSvcName(endpoints.Name))
			})
		})

		Context("and then original Endpoints resource is updated", func() {
			It("should update the cloned endpoints", func(ctx context.Context) {
				oldIP := "172.45.5.6" // defined in newEndpoints()
				newIP := "172.45.5.7"

				clonedEp := t.awaitEndpoints(ctx, controllers.GetInternalSvcName(endpoints.Name))

				// Confirm that both endpoints and clonedEP have oldIP
				t.awaitEndpointsHasIP(ctx, endpoints.Name, oldIP)
				t.awaitEndpointsHasIP(ctx, clonedEp.Name, oldIP)

				// Update endpoints to have newIP
				updatedEp := newEndpoints(endpoints.Name, newIP, endpoints.Labels)
				updatedEp = t.updateEndpoints(ctx, updatedEp)

				// Confirm that both endpoints and clonedEP have newIP
				t.awaitEndpointsHasIP(ctx, updatedEp.Name, newIP)
				t.awaitEndpointsHasIP(ctx, clonedEp.Name, newIP)
			})
		})
	})

	When("Endpoints resource is created after service is exported", func() {
		BeforeEach(func(ctx context.Context) {
			t.createService(ctx, service)
			t.createServiceExport(ctx, service)
			endpoints = t.createEndpoints(ctx, endpoints)
			t.awaitEndpoints(ctx, endpoints.Name)
		})

		It("should create an appropriate cloned Endpoints resource", func(ctx context.Context) {
			t.awaitEndpoints(ctx, controllers.GetInternalSvcName(endpoints.Name))
		})

		Context("and then original endpoints is deleted", func() {
			It("should delete the cloned endpoints", func(ctx context.Context) {
				t.awaitEndpoints(ctx, controllers.GetInternalSvcName(endpoints.Name))
				t.deleteEndpoints(ctx, endpoints)
				t.awaitNoEndpoints(ctx, controllers.GetInternalSvcName(endpoints.Name))
			})
		})

		Context("and then original Endpoints resource is updated", func() {
			It("should update the cloned Endpoints resource", func(ctx context.Context) {
				oldIP := "172.45.5.6" // defined in newEndpoints()
				newIP := "172.45.5.7"

				clonedEp := t.awaitEndpoints(ctx, controllers.GetInternalSvcName(endpoints.Name))

				// Confirm that both endpoints and clonedEP have oldIP
				t.awaitEndpointsHasIP(ctx, endpoints.Name, oldIP)
				t.awaitEndpointsHasIP(ctx, clonedEp.Name, oldIP)

				// Update endpoints to have newIP
				updatedEp := newEndpoints(endpoints.Name, newIP, endpoints.Labels)
				updatedEp = t.updateEndpoints(ctx, updatedEp)

				// Confirm that both endpoints and clonedEP have newIP
				t.awaitEndpointsHasIP(ctx, updatedEp.Name, newIP)
				t.awaitEndpointsHasIP(ctx, clonedEp.Name, newIP)
			})
		})
	})

	When("cloned Endpoints resource is created", func() {
		JustBeforeEach(func(ctx context.Context) {
			t.createService(ctx, service)
			t.createServiceExport(ctx, service)
			endpoints = t.createEndpoints(ctx, endpoints)
			t.awaitEndpoints(ctx, endpoints.Name)
			t.awaitEndpoints(ctx, controllers.GetInternalSvcName(endpoints.Name))
		})

		Context("and then controller is stopped", func() {
			It("should keep the cloned Endpoints resource on controller restart if the original still exists",
				func(ctx context.Context) {
					t.controller.Stop(ctx)

					time.Sleep(50 * time.Millisecond)
					t.awaitEndpoints(ctx, endpoints.Name)
					t.awaitEndpoints(ctx, controllers.GetInternalSvcName(endpoints.Name))

					// Restart controller
					t.start(ctx)

					time.Sleep(50 * time.Millisecond)
					t.awaitEndpoints(ctx, controllers.GetInternalSvcName(endpoints.Name))
				})

			It("should delete the cloned Endpoints resource on controller restart if the original has been deleted",
				func(ctx context.Context) {
					t.controller.Stop(ctx)

					time.Sleep(50 * time.Millisecond)
					t.awaitEndpoints(ctx, endpoints.Name)
					t.awaitEndpoints(ctx, controllers.GetInternalSvcName(endpoints.Name))

					// Delete original endpoints before restart controller
					t.deleteEndpoints(ctx, endpoints)
					t.awaitNoEndpoints(ctx, endpoints.Name)

					t.start(ctx)

					t.ensureNoEndpoints(ctx, controllers.GetInternalSvcName(endpoints.Name))
				})
		})
	})
}

func testHeadlessServiceWithoutSelector() {
	t := newServiceExportControllerTestDriver()

	var service *corev1.Service
	var endpoints *corev1.Endpoints

	BeforeEach(func(ctx context.Context) {
		service = newHeadlessServiceWithoutSelector()
		endpoints = newHeadlessServiceEndpoints(service.Name)
		t.createServiceExport(ctx, t.createService(ctx, service))
	})

	When("an endpoint for an exported Service is created", func() {
		BeforeEach(func(ctx context.Context) {
			t.createEndpoints(ctx, endpoints)
		})

		It("should create an appropriate GlobalIngressIP", func(ctx context.Context) {
			t.awaitHeadlessGlobalIngressIPForEP(ctx, service.Name, endpoints.Name)
		})

		Context("and then deleted", func() {
			It("should delete the GlobalIngressIP", func(ctx context.Context) {
				ingressIP := t.awaitHeadlessGlobalIngressIPForEP(ctx, service.Name, endpoints.Name)
				t.deleteEndpoints(ctx, endpoints)
				t.awaitNoGlobalIngressIP(ctx, ingressIP.Name)
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
		t.testDriverBase.initChains()
	})

	JustBeforeEach(func(ctx context.Context) {
		t.start(ctx)
	})

	AfterEach(func(ctx context.Context) {
		t.testDriverBase.afterEach(ctx)
	})

	return t
}

func (t *serviceExportControllerTestDriver) start(ctx context.Context,
) (*syncer.ResourceSyncerConfig, *controllers.IngressPodControllers, syncer.Interface) {
	var err error

	t.pool, err = ipam.NewIPPool(t.globalCIDR, metrics.GlobalnetMetricsReporter)
	Expect(err).To(Succeed())

	config := &syncer.ResourceSyncerConfig{
		SourceClient: t.dynClient,
		RestMapper:   t.restMapper,
		Scheme:       t.scheme,
	}

	podControllers, err := controllers.NewIngressPodControllers(config)
	Expect(err).To(Succeed())

	endpointsControllers, err := controllers.NewServiceExportEndpointsControllers(ctx, config)
	Expect(err).To(Succeed())

	ingressEndpointsControllers, err := controllers.NewIngressEndpointsControllers(config)
	Expect(err).To(Succeed())

	controller, err := controllers.NewServiceExportController(config, podControllers, endpointsControllers, ingressEndpointsControllers)
	t.controller = controller

	Expect(err).To(Succeed())
	Expect(t.controller.Start(ctx)).To(Succeed())

	testutil.AwaitWatchAction(&t.dynClient.Fake, "serviceexports")

	return config, podControllers, controller.GetSyncer()
}
