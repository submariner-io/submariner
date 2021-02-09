/*
© 2021 Red Hat, Inc. and others

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
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	fakeSubmClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned/fake"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers/ipam"
	"github.com/submariner-io/submariner/pkg/iptables"
	fakeIPT "github.com/submariner-io/submariner/pkg/iptables/fake"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	fakeK8sClientset "k8s.io/client-go/kubernetes/fake"
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
)

var _ = Describe("Endpoint monitoring", func() {
	t := newTestDriver()

	When("a local Endpoint is created then removed", func() {
		It("should update the appropriate IP table chains and start/stop monitoring resources", func() {
			service := t.createService(newService("nginx"))
			endpointName := t.createEndpoint(newEndpointSpec(clusterID, t.hostName, localCIDR))

			t.ipt.AwaitChain("nat", constants.SmGlobalnetIngressChain)
			t.ipt.AwaitChain("nat", constants.SmGlobalnetEgressChain)
			t.ipt.AwaitChain("nat", constants.SmPostRoutingChain)
			t.ipt.AwaitChain("nat", constants.SmGlobalnetMarkChain)

			t.awaitServiceGlobalIP(service.Name, localCIDR)

			Expect(t.submarinerClient.SubmarinerV1().Endpoints(namespace).Delete(endpointName, nil)).To(Succeed())

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

			Expect(t.submarinerClient.SubmarinerV1().Endpoints(namespace).Delete(endpointName, nil)).To(Succeed())
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

	JustBeforeEach(func() {
		service = t.createService(service)
		t.createEndpoint(newEndpointSpec(clusterID, t.hostName, localCIDR))
	})

	When("a Service without a global IP is created", func() {
		It("should assign a global IP and add an appropriate IP tables rule", func() {
			t.ipt.AwaitRule("nat", constants.SmGlobalnetIngressChain,
				ContainSubstring(t.awaitServiceGlobalIP(service.Name, localCIDR)))
		})
	})

	When("a Service's global IP is updated", func() {
		BeforeEach(func() {
			service.Annotations[ipam.SubmarinerIpamGlobalIP] = oldIP
		})

		It("should update the appropriate IP tables rule", func() {
			t.ipt.AwaitRule("nat", constants.SmGlobalnetIngressChain,
				ContainSubstring(service.Annotations[ipam.SubmarinerIpamGlobalIP]))

			service.Annotations[ipam.SubmarinerIpamGlobalIP] = newIP
			_, err := t.k8sClient.CoreV1().Services(namespace).Update(service)
			Expect(err).To(Succeed())
			t.ipt.AwaitRule("nat", constants.SmGlobalnetIngressChain,
				ContainSubstring(service.Annotations[ipam.SubmarinerIpamGlobalIP]))
		})
	})

	When("a Service with a global IP is removed", func() {
		It("should remove the appropriate IP tables rule", func() {
			globalIP := t.awaitServiceGlobalIP(service.Name, localCIDR)
			t.ipt.AwaitRule("nat", constants.SmGlobalnetIngressChain, ContainSubstring(globalIP))

			Expect(t.k8sClient.CoreV1().Services(namespace).Delete(service.Name, nil)).To(Succeed())
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
			pod.Annotations[ipam.SubmarinerIpamGlobalIP] = oldIP
		})

		It("should update the appropriate IP tables rule", func() {
			t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChain, ContainSubstring(pod.Annotations[ipam.SubmarinerIpamGlobalIP]))

			pod.Annotations[ipam.SubmarinerIpamGlobalIP] = newIP
			_, err := t.k8sClient.CoreV1().Pods(namespace).Update(pod)
			Expect(err).To(Succeed())
			t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChain, ContainSubstring(pod.Annotations[ipam.SubmarinerIpamGlobalIP]))
		})
	})

	When("a Pod with a global IP is removed", func() {
		It("should remove the appropriate IP tables rule", func() {
			globalIP := t.awaitPodGlobalIP(pod.Name)
			t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChain, ContainSubstring(globalIP))

			Expect(t.k8sClient.CoreV1().Pods(namespace).Delete(pod.Name, nil)).To(Succeed())
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
			_, err := t.k8sClient.CoreV1().Pods(namespace).Update(pod)
			Expect(err).To(Succeed())
			t.awaitPodGlobalIP(pod.Name)
		})
	})

	When("a Pod's IP is unset", func() {
		It("remove the appropriate IP tables rule", func() {
			globalIP := t.awaitPodGlobalIP(pod.Name)
			t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChain, ContainSubstring(globalIP))

			pod.Status.PodIP = ""
			_, err := t.k8sClient.CoreV1().Pods(namespace).Update(pod)
			Expect(err).To(Succeed())
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
				Annotations: map[string]string{constants.CniInterfaceIP: "10.20.30.40"},
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
				And(ContainSubstring(t.awaitNodeGlobalIP(node.Name)), ContainSubstring(node.Annotations[constants.CniInterfaceIP])))
		})

		When("it's also the local node", func() {
			It("should add an appropriate IP tables rule for health check", func() {
				t.ipt.AwaitRule("nat", constants.SmGlobalnetIngressChain, ContainSubstring(t.awaitNodeGlobalIP(node.Name)))
			})
		})
	})

	When("a Node's global IP is updated", func() {
		BeforeEach(func() {
			node.Annotations[ipam.SubmarinerIpamGlobalIP] = oldIP
		})

		It("should update the appropriate IP tables rule", func() {
			t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChain, ContainSubstring(node.Annotations[ipam.SubmarinerIpamGlobalIP]))

			node.Annotations[ipam.SubmarinerIpamGlobalIP] = newIP
			_, err := t.k8sClient.CoreV1().Nodes().Update(node)
			Expect(err).To(Succeed())
			t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChain, ContainSubstring(node.Annotations[ipam.SubmarinerIpamGlobalIP]))
		})
	})

	When("a Node with a global IP is removed", func() {
		It("should remove the appropriate IP tables rule", func() {
			globalIP := t.awaitNodeGlobalIP(node.Name)
			t.ipt.AwaitRule("nat", constants.SmGlobalnetEgressChain, ContainSubstring(globalIP))

			Expect(t.k8sClient.CoreV1().Nodes().Delete(node.Name, nil)).To(Succeed())
			t.ipt.AwaitNoRule("nat", constants.SmGlobalnetEgressChain, ContainSubstring(globalIP))
		})
	})

	When(fmt.Sprintf("a Node is initially created without the %q annotation then is subsequently set",
		constants.CniInterfaceIP), func() {
		BeforeEach(func() {
			delete(node.Annotations, constants.CniInterfaceIP)
		})

		It("should eventually assign a global IP", func() {
			t.awaitNoNodeGlobalIP(node)

			node.Annotations[constants.CniInterfaceIP] = "10.20.30.40"
			_, err := t.k8sClient.CoreV1().Nodes().Update(node)
			Expect(err).To(Succeed())
			t.awaitNodeGlobalIP(node.Name)
		})
	})
})

type testDriver struct {
	spec             *ipam.SubmarinerIpamControllerSpecification
	gatewayMonitor   *ipam.GatewayMonitor
	submarinerClient submarinerClientset.Interface
	k8sClient        kubernetes.Interface
	ipt              *fakeIPT.IPTables
	hostName         string
	stopCh           chan struct{}
}

func newTestDriver() *testDriver {
	t := &testDriver{}

	BeforeEach(func() {
		t.spec = &ipam.SubmarinerIpamControllerSpecification{
			ClusterID:  clusterID,
			Namespace:  namespace,
			GlobalCIDR: []string{localCIDR},
		}

		t.ipt = fakeIPT.New()

		iptables.NewFunc = func() (iptables.Interface, error) {
			return t.ipt, nil
		}

		t.ipt.AddChainsFor("nat", "KUBE-SERVICES")

		t.submarinerClient = fakeSubmClientset.NewSimpleClientset()
		t.k8sClient = fakeK8sClientset.NewSimpleClientset()
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

	t.gatewayMonitor, err = ipam.NewGatewayMonitor(t.spec, t.submarinerClient, t.k8sClient)

	Expect(err).To(Succeed())

	Expect(t.gatewayMonitor.Start(t.stopCh)).To(Succeed())
	t.ipt.AwaitChain("nat", constants.SmGlobalnetMarkChain)
}

func (t *testDriver) createEndpoint(spec *submarinerv1.EndpointSpec) string {
	endpointName, err := util.GetEndpointCRDNameFromParams(spec.ClusterID, spec.CableName)
	Expect(err).To(Succeed())

	_, err = t.submarinerClient.SubmarinerV1().Endpoints(namespace).Create(&submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: endpointName,
		},
		Spec: *spec,
	})

	Expect(err).To(Succeed())

	return endpointName
}

func (t *testDriver) createService(s *corev1.Service) *corev1.Service {
	service, err := t.k8sClient.CoreV1().Services(nsOrDefault(s.Namespace)).Create(s)
	Expect(err).To(Succeed())

	if len(service.Spec.Ports[0].Name) > 0 {
		hash := sha256.Sum256([]byte(service.GetNamespace() + "/" + service.GetName() + ":" + service.Spec.Ports[0].Name +
			strings.ToLower(string(service.Spec.Ports[0].Protocol))))
		encoded := base32.StdEncoding.EncodeToString(hash[:])
		t.ipt.AddChainsFor("nat", "KUBE-SVC-"+encoded[:16])
	}

	return service
}

func (t *testDriver) awaitServiceGlobalIP(name, cidr string) string {
	return t.awaitGlobalIP(name, cidr, func(string) (runtime.Object, error) {
		return t.k8sClient.CoreV1().Services(namespace).Get(name, metav1.GetOptions{})
	})
}

func (t *testDriver) awaitNoServiceGlobalIP(s *corev1.Service) {
	t.awaitNoGlobalIP(s.Name, func(string) (runtime.Object, error) {
		return t.k8sClient.CoreV1().Services(nsOrDefault(nsOrDefault(s.Namespace))).Get(s.Name, metav1.GetOptions{})
	})
}

func (t *testDriver) awaitGlobalIP(name, cidr string, getter func(string) (runtime.Object, error)) string {
	var globalIP string

	Eventually(func() string {
		obj, err := getter(name)
		Expect(err).To(Succeed())

		metaObj, err := meta.Accessor(obj)
		Expect(err).To(Succeed())

		globalIP = metaObj.GetAnnotations()[ipam.SubmarinerIpamGlobalIP]
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

		return metaObj.GetAnnotations()[ipam.SubmarinerIpamGlobalIP]
	}, 500*time.Millisecond).Should(BeEmpty())
}

func (t *testDriver) createPod(p *corev1.Pod) *corev1.Pod {
	pod, err := t.k8sClient.CoreV1().Pods(nsOrDefault(p.Namespace)).Create(p)
	Expect(err).To(Succeed())

	return pod
}

func (t *testDriver) awaitPodGlobalIP(name string) string {
	return t.awaitGlobalIP(name, localCIDR, func(string) (runtime.Object, error) {
		return t.k8sClient.CoreV1().Pods(namespace).Get(name, metav1.GetOptions{})
	})
}

func (t *testDriver) awaitNoPodGlobalIP(pod *corev1.Pod) {
	t.awaitNoGlobalIP(pod.Name, func(string) (runtime.Object, error) {
		return t.k8sClient.CoreV1().Pods(nsOrDefault(pod.Namespace)).Get(pod.Name, metav1.GetOptions{})
	})
}

func (t *testDriver) createNode(node *corev1.Node) *corev1.Node {
	node, err := t.k8sClient.CoreV1().Nodes().Create(node)
	Expect(err).To(Succeed())

	return node
}

func (t *testDriver) awaitNodeGlobalIP(name string) string {
	return t.awaitGlobalIP(name, localCIDR, func(string) (runtime.Object, error) {
		return t.k8sClient.CoreV1().Nodes().Get(name, metav1.GetOptions{})
	})
}

func (t *testDriver) awaitNoNodeGlobalIP(node *corev1.Node) {
	t.awaitNoGlobalIP(node.Name, func(string) (runtime.Object, error) {
		return t.k8sClient.CoreV1().Nodes().Get(node.Name, metav1.GetOptions{})
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
