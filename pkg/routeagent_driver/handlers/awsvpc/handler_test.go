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

package awsvpc_test

import (
	"context"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/event"
	evtesting "github.com/submariner-io/submariner/pkg/event/testing"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	fakeNetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/awsvpc"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/kubeproxy"
	"github.com/vishvananda/netlink"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	k8snet "k8s.io/utils/net"
)

const (
	gatewayNodeName = "gateway-node"
	workerNodeName  = "worker-a"
	workerNodeIP    = "10.151.71.78"
	workerVTEP      = "240.151.71.78"
	podIP           = "10.151.70.157"
	vxlanIndex      = 99
)

func init() {
	kzerolog.AddFlags(nil)
}

var _ = BeforeSuite(func() {
	kzerolog.InitK8sLogging()
})

func TestAWSVPC(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "AWS VPC CNI Handler Suite")
}

var _ = Describe("Amazon VPC CNI handler", func() {
	t := newTestDriver()

	Specify("GetNetworkPlugins should return only amazon-vpc-cni", func() {
		Expect(t.handler.GetNetworkPlugins()).To(Equal([]string{cni.AmazonVPCCNI}))
	})

	It("should program a pod /32 via the worker VTEP on the gateway", func(ctx context.Context) {
		t.addNode(gatewayNodeName, "10.151.4.10")
		t.addNode(workerNodeName, workerNodeIP)
		t.addPod("p1", workerNodeName, podIP)

		t.Start(ctx)
		t.CreateLocalHostEndpoint(ctx)

		t.netLink.AwaitDstRoutes(vxlanIndex, 0, podIP+"/32")

		routes, err := t.netLink.RouteList(t.vxlanLink, k8snet.IPv4)
		Expect(err).NotTo(HaveOccurred())

		found := false

		for i := range routes {
			if routes[i].Dst != nil && routes[i].Dst.String() == podIP+"/32" {
				Expect(routes[i].Gw.String()).To(Equal(workerVTEP))

				found = true
			}
		}
		Expect(found).To(BeTrue())
	})

	It("should program a node InternalIP /32 via VTEP in the PBR table, not main", func(ctx context.Context) {
		t.addNode(gatewayNodeName, "10.151.4.10")
		t.addNode(workerNodeName, workerNodeIP)

		t.Start(ctx)
		t.CreateLocalHostEndpoint(ctx)

		t.netLink.AwaitDstRoutes(vxlanIndex, 152, workerNodeIP+"/32")
		t.netLink.AwaitNoDstRoutes(vxlanIndex, 0, workerNodeIP+"/32")

		routes, err := t.netLink.RouteList(t.vxlanLink, k8snet.IPv4)
		Expect(err).NotTo(HaveOccurred())

		found := false

		for i := range routes {
			if routes[i].Dst != nil && routes[i].Dst.String() == workerNodeIP+"/32" {
				Expect(routes[i].Gw.String()).To(Equal(workerVTEP))
				Expect(routes[i].Table).To(Equal(152))

				found = true
			}
		}
		Expect(found).To(BeTrue())
	})

	It("should ignore pods on the local gateway node", func(ctx context.Context) {
		t.addNode(gatewayNodeName, "10.151.4.10")
		t.addPod("local-pod", gatewayNodeName, "10.151.70.1")

		t.Start(ctx)
		t.CreateLocalHostEndpoint(ctx)

		t.netLink.AwaitNoDstRoutes(vxlanIndex, 0, "10.151.70.1/32")
		// Local gateway node IP must not get a VTEP route either.
		t.netLink.AwaitNoDstRoutes(vxlanIndex, 0, "10.151.4.10/32")
	})
})

type testDriver struct {
	*evtesting.ControllerSupport
	handler   event.Handler
	clientset *fake.Clientset
	netLink   *fakeNetlink.NetLink
	vxlanLink *netlink.Vxlan
}

func newTestDriver() *testDriver {
	t := &testDriver{
		ControllerSupport: evtesting.NewControllerSupport(),
	}

	BeforeEach(func() {
		Expect(os.Setenv("NODE_NAME", gatewayNodeName)).To(Succeed())

		t.clientset = fake.NewSimpleClientset()
		t.netLink = fakeNetlink.New()
		t.netLink.SetLinkIndex(kubeproxy.VxLANIface, vxlanIndex)

		netlinkAPI.NewFunc = func() netlinkAPI.Interface {
			return t.netLink
		}

		t.vxlanLink = &netlink.Vxlan{
			LinkAttrs: netlink.LinkAttrs{Name: kubeproxy.VxLANIface, Index: vxlanIndex},
		}
		Expect(t.netLink.LinkAdd(t.vxlanLink)).To(Succeed())

		t.handler = awsvpc.NewHandler(t.clientset, k8snet.IPv4)
	})

	AfterEach(func() {
		_ = t.handler.Stop(context.TODO())
		_ = os.Unsetenv("NODE_NAME")
		netlinkAPI.NewFunc = nil
	})

	return t
}

func (t *testDriver) Start(ctx context.Context) {
	t.ControllerSupport.Start(ctx, t.handler)
}

func (t *testDriver) addNode(name, ip string) {
	_, err := t.clientset.CoreV1().Nodes().Create(context.TODO(), &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: ip}},
		},
	}, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred())
}

func (t *testDriver) addPod(name, nodeName, ip string) {
	_, err := t.clientset.CoreV1().Pods("default").Create(context.TODO(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: nodeName},
		Status: corev1.PodStatus{
			Phase:  corev1.PodRunning,
			PodIP:  ip,
			PodIPs: []corev1.PodIP{{IP: ip}},
		},
	}, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred())
}
