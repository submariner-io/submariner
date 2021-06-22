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
package ipam_test

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers/ipam"
	"github.com/submariner-io/submariner/pkg/iptables"
	fakeIPT "github.com/submariner-io/submariner/pkg/iptables/fake"
	routeAgent "github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	fakeDynClient "k8s.io/client-go/dynamic/fake"
	mcsv1a1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

const (
	clusterID       = "east"
	remoteClusterID = "west"
	namespace       = "submariner"
	excludedNS      = "excludedNS"
	localCIDR       = "169.254.1.0/24"
	remoteCIDR      = "169.254.2.0/24"
	nodeName        = "raiders"
	oldIP           = "169.254.1.10"
	newIP           = "169.254.1.20"
	globalIPSubstr  = "169.254.1"
)

var _ = Describe("Endpoint monitoring", func() {
	t := newTestDriver()

	When("a local Endpoint is created then removed", func() {
		It("should update the appropriate IP table chains and start/stop monitoring resources", func() {
			service := t.createService(newService("nginx"))
			t.createSvcExport(service)
			endpointName := t.createEndpoint(newEndpointSpec(clusterID, t.hostName, localCIDR))

			t.ipt.AwaitChain("nat", constants.SmGlobalnetIngressChain)
			t.ipt.AwaitChain("nat", constants.SmGlobalnetEgressChain)
			t.ipt.AwaitChain("nat", routeAgent.SmPostRoutingChain)
			t.ipt.AwaitChain("nat", constants.SmGlobalnetMarkChain)

			t.awaitServiceGlobalIP(service.Name)

			Expect(t.endpoints.Delete(context.TODO(), endpointName, metav1.DeleteOptions{})).To(Succeed())

			t.ipt.AwaitNoChain("nat", constants.SmGlobalnetIngressChain)
			t.ipt.AwaitNoChain("nat", constants.SmGlobalnetEgressChain)
			t.ipt.AwaitNoChain("nat", constants.SmGlobalnetMarkChain)

			t.awaitNoServiceGlobalIP(t.createService(newService("nginx2")))
		})
	})

	When("a remote Endpoint with non-overlapping CIDRs is created then removed", func() {
		It("should add/remove appropriate IP table rule(s)", func() {
			endpointName := t.createEndpoint(newEndpointSpec(remoteClusterID, t.hostName, remoteCIDR))
			t.ipt.AwaitRule("nat", constants.SmGlobalnetMarkChain, ContainSubstring(remoteCIDR))

			Expect(t.endpoints.Delete(context.TODO(), endpointName, metav1.DeleteOptions{})).To(Succeed())
			t.ipt.AwaitNoRule("nat", constants.SmGlobalnetMarkChain, ContainSubstring(remoteCIDR))
		})
	})

	When("a remote Endpoint with an overlapping CIDR is created", func() {
		It("should not add expected IP table rule(s)", func() {
			t.createEndpoint(newEndpointSpec(remoteClusterID, t.hostName, localCIDR))
			time.Sleep(500 * time.Millisecond)
			t.ipt.AwaitNoRule("nat", constants.SmGlobalnetMarkChain, ContainSubstring(localCIDR))
		})
	})
})

var _ = Describe("Service monitoring", func() {
	t := newTestDriver()

	var service *corev1.Service

	BeforeEach(func() {
		service = newService("nginx")
	})

	When("a Service without a global IP is created", func() {
		JustBeforeEach(func() {
			t.createEndpoint(newEndpointSpec(clusterID, t.hostName, localCIDR))
		})

		Context("and it was already exported", func() {
			It("should assign a global IP and add an appropriate IP tables rule", func() {
				t.createSvcExport(service)
				time.Sleep(300 * time.Millisecond)
				t.ipt.AwaitNoRule("nat", constants.SmGlobalnetIngressChain, ContainSubstring(globalIPSubstr))

				t.createService(service)
				globalIP := t.awaitServiceGlobalIP(service.Name)
				t.ipt.AwaitRule("nat", constants.SmGlobalnetIngressChain, ContainSubstring(globalIP))
			})
		})

		Context("and is subsequently exported", func() {
			It("should eventually assign a global IP and add an appropriate IP tables rule", func() {
				service = t.createService(service)
				t.awaitNoServiceGlobalIP(service)
				t.ipt.AwaitNoRule("nat", constants.SmGlobalnetIngressChain, ContainSubstring(globalIPSubstr))

				t.createSvcExport(service)
				globalIP := t.awaitServiceGlobalIP(service.Name)
				t.ipt.AwaitRule("nat", constants.SmGlobalnetIngressChain, ContainSubstring(globalIP))
			})
		})
	})

	Context("", func() {
		JustBeforeEach(func() {
			service = t.createService(service)
			t.createSvcExport(service)
			t.createEndpoint(newEndpointSpec(clusterID, t.hostName, localCIDR))
		})

		When("a Service's global IP is updated", func() {
			BeforeEach(func() {
				service.Annotations[ipam.SubmarinerIPAMGlobalIP] = oldIP
			})

			It("should update the appropriate IP tables rule", func() {
				t.ipt.AwaitRule("nat", constants.SmGlobalnetIngressChain,
					ContainSubstring(service.Annotations[ipam.SubmarinerIPAMGlobalIP]))

				service.Annotations[ipam.SubmarinerIPAMGlobalIP] = newIP
				test.UpdateResource(t.services.Namespace(namespace), service)
				t.ipt.AwaitRule("nat", constants.SmGlobalnetIngressChain,
					ContainSubstring(service.Annotations[ipam.SubmarinerIPAMGlobalIP]))
			})
		})

		When("a Service with a global IP is unexported", func() {
			It("should unassign its global IP and remove the appropriate IP tables rule", func() {
				globalIP := t.awaitServiceGlobalIP(service.Name)
				t.ipt.AwaitRule("nat", constants.SmGlobalnetIngressChain, ContainSubstring(globalIP))

				Expect(t.serviceExports.Namespace(namespace).Delete(context.TODO(), service.Name, metav1.DeleteOptions{})).To(Succeed())
				t.ipt.AwaitNoRule("nat", constants.SmGlobalnetIngressChain, ContainSubstring(globalIPSubstr))
			})
		})

		When("a Service with a global IP is removed", func() {
			It("should remove the appropriate IP tables rule", func() {
				globalIP := t.awaitServiceGlobalIP(service.Name)
				t.ipt.AwaitRule("nat", constants.SmGlobalnetIngressChain, ContainSubstring(globalIP))

				Expect(t.services.Namespace(namespace).Delete(context.TODO(), service.Name, metav1.DeleteOptions{})).To(Succeed())
				t.ipt.AwaitNoRule("nat", constants.SmGlobalnetIngressChain, ContainSubstring(globalIP))
			})
		})

		When("a Service in an excluded namespace is created", func() {
			BeforeEach(func() {
				service.Namespace = excludedNS
				t.spec.ExcludeNS = []string{excludedNS}
			})

			It("should not assign a global IP", func() {
				t.awaitNoServiceGlobalIP(service)
			})
		})

		When("a non-ClusterIP Service is created", func() {
			BeforeEach(func() {
				service.Spec.Type = corev1.ServiceTypeNodePort
			})

			It("should not assign a global IP", func() {
				t.awaitNoServiceGlobalIP(service)
			})
		})

		When("a Service with no associated kube-proxy IP tables chain is created", func() {
			BeforeEach(func() {
				service.Spec.Ports[0].Name = ""
			})

			It("should not assign a global IP", func() {
				t.awaitNoServiceGlobalIP(service)
			})
		})
	})
})

var _ = Describe("Pod monitoring", func() {
	t := newTestDriver()

	var pod *corev1.Pod

	BeforeEach(func() {
		pod = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "nginx",
				Annotations: map[string]string{},
			},
			Status: corev1.PodStatus{
				PodIP: "1.2.3.4",
			},
		}
	})

	JustBeforeEach(func() {
		pod = t.createPod(pod)
		t.createEndpoint(newEndpointSpec(clusterID, t.hostName, localCIDR))
	})

	When("a Pod without a global IP is created", func() {
		It("should assign a global IP and add an appropriate IP tables rule", func() {
			t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChain,
				And(ContainSubstring(t.awaitPodGlobalIP(pod.Name)), ContainSubstring(pod.Status.PodIP)))

		})
	})

	When("a Pod's global IP is updated", func() {
		BeforeEach(func() {
			pod.Annotations[ipam.SubmarinerIPAMGlobalIP] = oldIP
		})

		It("should update the appropriate IP tables rule", func() {
			t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChain, ContainSubstring(pod.Annotations[ipam.SubmarinerIPAMGlobalIP]))

			pod.Annotations[ipam.SubmarinerIPAMGlobalIP] = newIP
			test.UpdateResource(t.pods.Namespace(namespace), pod)
			t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChain, ContainSubstring(pod.Annotations[ipam.SubmarinerIPAMGlobalIP]))
		})
	})

	When("a Pod with a global IP is removed", func() {
		It("should remove the appropriate IP tables rule", func() {
			globalIP := t.awaitPodGlobalIP(pod.Name)
			t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChain, ContainSubstring(globalIP))

			Expect(t.pods.Namespace(namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})).To(Succeed())
			t.ipt.AwaitNoRule("nat", constants.SmGlobalnetEgressChain, ContainSubstring(globalIP))
		})
	})

	When("a Pod is initially created without an IP then is subsequently set", func() {
		BeforeEach(func() {
			pod.Status.PodIP = ""
		})

		It("should eventually assign a global IP", func() {
			t.awaitNoPodGlobalIP(pod)

			pod.Status.PodIP = "1.2.3.4"
			test.UpdateResource(t.pods.Namespace(namespace), pod)
			t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChain,
				And(ContainSubstring(t.awaitPodGlobalIP(pod.Name)), ContainSubstring(pod.Status.PodIP)))
		})
	})

	When("a Pod's IP is unset", func() {
		It("remove the appropriate IP tables rule", func() {
			globalIP := t.awaitPodGlobalIP(pod.Name)
			t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChain, ContainSubstring(globalIP))

			pod.Status.PodIP = ""
			test.UpdateResource(t.pods.Namespace(namespace), pod)
			t.ipt.AwaitNoRule("nat", constants.SmGlobalnetEgressChain, ContainSubstring(globalIP))
		})
	})

	When("a Pod with host networking enabled is created", func() {
		BeforeEach(func() {
			pod.Spec.HostNetwork = true
		})

		It("should not assign a global IP", func() {
			t.awaitNoPodGlobalIP(pod)
		})
	})

	When("a Pod in an excluded namespace is created", func() {
		BeforeEach(func() {
			pod.Namespace = excludedNS
			t.spec.ExcludeNS = []string{excludedNS}
		})

		It("should not assign a global IP", func() {
			t.awaitNoPodGlobalIP(pod)
		})
	})
})

var _ = Describe("Node monitoring", func() {
	t := newTestDriver()

	var node *corev1.Node

	BeforeEach(func() {
		node = &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:        nodeName,
				Annotations: map[string]string{routeAgent.CNIInterfaceIP: "10.20.30.40"},
			},
		}
	})

	JustBeforeEach(func() {
		node = t.createNode(node)
		t.createEndpoint(newEndpointSpec(clusterID, t.hostName, localCIDR))
	})

	When("a Node without a global IP is created", func() {
		It("should assign a global IP and add an appropriate IP tables rule", func() {
			t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChain,
				And(ContainSubstring(t.awaitNodeGlobalIP(node.Name)), ContainSubstring(node.Annotations[routeAgent.CNIInterfaceIP])))
		})

		Context("and it's also the local node", func() {
			It("should add an appropriate IP tables rule for health check", func() {
				t.ipt.AwaitRule("nat", constants.SmGlobalnetIngressChain, ContainSubstring(t.awaitNodeGlobalIP(node.Name)))
			})
		})
	})

	When("a Node's global IP is updated", func() {
		BeforeEach(func() {
			node.Annotations[ipam.SubmarinerIPAMGlobalIP] = oldIP
		})

		It("should update the appropriate IP tables rule", func() {
			t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChain, ContainSubstring(node.Annotations[ipam.SubmarinerIPAMGlobalIP]))

			node.Annotations[ipam.SubmarinerIPAMGlobalIP] = newIP
			test.UpdateResource(t.nodes, node)
			t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChain, ContainSubstring(node.Annotations[ipam.SubmarinerIPAMGlobalIP]))
		})
	})

	When("a Node with a global IP is removed", func() {
		It("should remove the appropriate IP tables rule", func() {
			globalIP := t.awaitNodeGlobalIP(node.Name)
			t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChain, ContainSubstring(globalIP))

			Expect(t.nodes.Delete(context.TODO(), node.Name, metav1.DeleteOptions{})).To(Succeed())
			t.ipt.AwaitNoRule("nat", constants.SmGlobalnetEgressChain, ContainSubstring(globalIP))
		})
	})

	When(fmt.Sprintf("a Node is initially created without the %q annotation then is subsequently set",
		routeAgent.CNIInterfaceIP), func() {
		BeforeEach(func() {
			delete(node.Annotations, routeAgent.CNIInterfaceIP)
		})

		It("should eventually assign a global IP", func() {
			t.awaitNoNodeGlobalIP(node)

			node.SetAnnotations(map[string]string{routeAgent.CNIInterfaceIP: "10.20.30.40"})
			test.UpdateResource(t.nodes, node)
			t.awaitNodeGlobalIP(node.Name)
		})
	})
})

type testDriver struct {
	spec           *ipam.SubmarinerIPAMControllerSpecification
	config         watcher.Config
	endpoints      dynamic.ResourceInterface
	services       dynamic.NamespaceableResourceInterface
	serviceExports dynamic.NamespaceableResourceInterface
	pods           dynamic.NamespaceableResourceInterface
	nodes          dynamic.NamespaceableResourceInterface
	gatewayMonitor *ipam.GatewayMonitor
	dynClient      dynamic.Interface
	ipt            *fakeIPT.IPTables
	hostName       string
	stopCh         chan struct{}
}

func newTestDriver() *testDriver {
	t := &testDriver{}

	BeforeEach(func() {
		t.spec = &ipam.SubmarinerIPAMControllerSpecification{
			ClusterID:  clusterID,
			Namespace:  namespace,
			GlobalCIDR: []string{localCIDR},
		}

		t.ipt = fakeIPT.New()

		iptables.NewFunc = func() (iptables.Interface, error) {
			return t.ipt, nil
		}

		t.ipt.AddChainsFor("nat", "KUBE-SERVICES")

		scheme := runtime.NewScheme()
		Expect(mcsv1a1.AddToScheme(scheme)).To(Succeed())
		Expect(submarinerv1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		t.dynClient = fakeDynClient.NewSimpleDynamicClient(scheme)

		t.config = watcher.Config{
			RestMapper: test.GetRESTMapperFor(&submarinerv1.Endpoint{}, &corev1.Service{}, &corev1.Node{}, &corev1.Pod{},
				&mcsv1a1.ServiceExport{}),
			Client: t.dynClient,
			Scheme: scheme,
		}

		t.endpoints = t.dynClient.Resource(*test.GetGroupVersionResourceFor(t.config.RestMapper, &submarinerv1.Endpoint{})).
			Namespace(namespace)

		t.services = t.dynClient.Resource(*test.GetGroupVersionResourceFor(t.config.RestMapper, &corev1.Service{}))

		t.serviceExports = t.dynClient.Resource(*test.GetGroupVersionResourceFor(t.config.RestMapper, &mcsv1a1.ServiceExport{}))

		t.pods = t.dynClient.Resource(*test.GetGroupVersionResourceFor(t.config.RestMapper, &corev1.Pod{}))

		t.nodes = t.dynClient.Resource(*test.GetGroupVersionResourceFor(t.config.RestMapper, &corev1.Node{}))
	})

	JustBeforeEach(func() {
		t.start()
	})

	AfterEach(func() {
		close(t.stopCh)
		t.gatewayMonitor.Stop()
		iptables.NewFunc = nil
	})

	return t
}

func (t *testDriver) start() {
	os.Setenv("NODE_NAME", nodeName)

	var err error

	t.hostName, err = os.Hostname()
	Expect(err).To(Succeed())

	t.stopCh = make(chan struct{})

	t.gatewayMonitor, err = ipam.NewGatewayMonitor(t.spec, t.config)

	Expect(err).To(Succeed())

	Expect(t.gatewayMonitor.Start(t.stopCh)).To(Succeed())
	t.ipt.AwaitChain("nat", constants.SmGlobalnetMarkChain)
}

func (t *testDriver) createEndpoint(spec *submarinerv1.EndpointSpec) string {
	endpointName, err := util.GetEndpointCRDNameFromParams(spec.ClusterID, spec.CableName)
	Expect(err).To(Succeed())

	test.CreateResource(t.endpoints, &submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: endpointName,
		},
		Spec: *spec,
	})

	return endpointName
}

func (t *testDriver) createService(service *corev1.Service) *corev1.Service {
	ns := nsOrDefault(service.Namespace)
	test.CreateResource(t.services.Namespace(ns), service)

	if len(service.Spec.Ports[0].Name) > 0 {
		hash := sha256.Sum256([]byte(ns + "/" + service.GetName() + ":" + service.Spec.Ports[0].Name +
			strings.ToLower(string(service.Spec.Ports[0].Protocol))))
		encoded := base32.StdEncoding.EncodeToString(hash[:])
		t.ipt.AddChainsFor("nat", "KUBE-SVC-"+encoded[:16])
	}

	return service
}

func (t *testDriver) createSvcExport(s *corev1.Service) {
	svcEx := newServiceExport(s.GetNamespace(), s.GetName())
	test.CreateResource(t.serviceExports.Namespace(svcEx.GetNamespace()), svcEx)
}

func (t *testDriver) awaitServiceGlobalIP(name string) string {
	return t.awaitGlobalIP(name, localCIDR, func(string) (runtime.Object, error) {
		return t.services.Namespace(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	})
}

func (t *testDriver) awaitNoServiceGlobalIP(s *corev1.Service) {
	t.awaitNoGlobalIP(s.Name, func(string) (runtime.Object, error) {
		return t.services.Namespace(nsOrDefault(s.Namespace)).Get(context.TODO(), s.Name, metav1.GetOptions{})
	})
}

func (t *testDriver) awaitGlobalIP(name, cidr string, getter func(string) (runtime.Object, error)) string {
	var globalIP string

	Eventually(func() string {
		obj, err := getter(name)
		Expect(err).To(Succeed())

		metaObj, err := meta.Accessor(obj)
		Expect(err).To(Succeed())

		globalIP = metaObj.GetAnnotations()[ipam.SubmarinerIPAMGlobalIP]
		return globalIP
	}, 5).ShouldNot(BeEmpty())

	Expect(isValidIPForCIDR(cidr, globalIP)).To(BeTrue(), "Returned global IP %q is not valid for CIDR %q", globalIP, cidr)

	return globalIP
}

func (t *testDriver) awaitNoGlobalIP(name string, getter func(string) (runtime.Object, error)) {
	Consistently(func() string {
		obj, err := getter(name)
		Expect(err).To(Succeed())

		metaObj, err := meta.Accessor(obj)
		Expect(err).To(Succeed())

		return metaObj.GetAnnotations()[ipam.SubmarinerIPAMGlobalIP]
	}, 500*time.Millisecond).Should(BeEmpty())
}

func (t *testDriver) createPod(p *corev1.Pod) *corev1.Pod {
	return fromUnstructured(test.CreateResource(t.pods.Namespace(nsOrDefault(p.Namespace)), p), &corev1.Pod{}).(*corev1.Pod)
}

func (t *testDriver) awaitPodGlobalIP(name string) string {
	return t.awaitGlobalIP(name, localCIDR, func(string) (runtime.Object, error) {
		return t.pods.Namespace(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	})
}

func (t *testDriver) awaitNoPodGlobalIP(pod *corev1.Pod) {
	t.awaitNoGlobalIP(pod.Name, func(string) (runtime.Object, error) {
		return t.pods.Namespace(nsOrDefault(pod.Namespace)).Get(context.TODO(), pod.Name, metav1.GetOptions{})
	})
}

func (t *testDriver) createNode(node *corev1.Node) *corev1.Node {
	return fromUnstructured(test.CreateResource(t.nodes, node), &corev1.Node{}).(*corev1.Node)
}

func (t *testDriver) awaitNodeGlobalIP(name string) string {
	return t.awaitGlobalIP(name, localCIDR, func(string) (runtime.Object, error) {
		return t.nodes.Get(context.TODO(), name, metav1.GetOptions{})
	})
}

func (t *testDriver) awaitNoNodeGlobalIP(node *corev1.Node) {
	t.awaitNoGlobalIP(node.Name, func(string) (runtime.Object, error) {
		return t.nodes.Get(context.TODO(), node.Name, metav1.GetOptions{})
	})
}

func newService(name string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: map[string]string{},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:     "eth0",
					Protocol: corev1.ProtocolTCP,
				},
			},
			ClusterIP: "1.2.3.4",
			Type:      corev1.ServiceTypeClusterIP,
		},
	}
}

func newEndpointSpec(clusterID, hostname, subnet string) *submarinerv1.EndpointSpec {
	return &submarinerv1.EndpointSpec{
		CableName: fmt.Sprintf("submariner-cable-%s-192-68-1-2", clusterID),
		ClusterID: clusterID,
		PrivateIP: "192-68-1-2",
		Hostname:  hostname,
		Subnets:   []string{subnet},
	}
}

func newServiceExport(namespace, name string) *unstructured.Unstructured {
	resourceServiceExport := &unstructured.Unstructured{}
	resourceServiceExport.SetName(name)
	resourceServiceExport.SetNamespace(nsOrDefault(namespace))
	resourceServiceExport.SetKind("ServiceExport")
	resourceServiceExport.SetAPIVersion("multicluster.x-k8s.io/v1alpha1")

	return resourceServiceExport
}

func isValidIPForCIDR(cidr, ip string) bool {
	_, ipnet, err := net.ParseCIDR(cidr)
	Expect(err).NotTo(HaveOccurred())

	return ipnet.Contains(net.ParseIP(ip))
}

func nsOrDefault(ns string) string {
	if ns == "" {
		return namespace
	}

	return ns
}

func fromUnstructured(from *unstructured.Unstructured, to runtime.Object) runtime.Object {
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(from.Object, to)
	Expect(err).To(Succeed())

	return to
}
