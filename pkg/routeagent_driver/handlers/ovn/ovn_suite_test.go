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

package ovn_test

import (
	"context"
	"encoding/json"
	"flag"
	"net"
	"os"
	"sort"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	fakesubm "github.com/submariner-io/submariner/pkg/client/clientset/versioned/fake"
	eventtesting "github.com/submariner-io/submariner/pkg/event/testing"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	fakenetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	nodeutil "github.com/submariner-io/submariner/pkg/node"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/ovn"
	"github.com/vishvananda/netlink"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	fakek8s "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	k8snet "k8s.io/utils/net"
)

const (
	OVNK8sMgmntIntIndex = 99
	remoteClusterID     = "remote-cluster"
)

func init() {
	flags := flag.NewFlagSet("kzerolog", flag.ExitOnError)
	kzerolog.AddFlags(flags)
	_ = flags.Parse([]string{"-v=2"})

	kzerolog.AddFlags(nil)
}

var _ = BeforeSuite(func() {
	kzerolog.InitK8sLogging()
	Expect(submV1.AddToScheme(scheme.Scheme)).To(Succeed())
})

func TestOvn(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Ovn Suite")
}

type testDriver struct {
	*eventtesting.ControllerSupport
	submClient         *fakesubm.Clientset
	k8sClient          *fakek8s.Clientset
	dynClient          *fakedynamic.FakeDynamicClient
	netLink            *fakenetlink.NetLink
	transitSwitchIP    map[k8snet.IPFamily]string
	OVNK8sMgmntIntCIDR map[k8snet.IPFamily]*net.IPNet
	node               *corev1.Node
}

func newTestDriver() *testDriver {
	t := &testDriver{
		ControllerSupport: eventtesting.NewControllerSupport(),
	}

	BeforeEach(func() {
		t.transitSwitchIP = map[k8snet.IPFamily]string{k8snet.IPv4: "190.1.2.0", k8snet.IPv6: "1a00:200::"}
		t.submClient = fakesubm.NewSimpleClientset()
		t.k8sClient = fakek8s.NewClientset()
		t.dynClient = fakedynamic.NewSimpleDynamicClient(scheme.Scheme)
		t.OVNK8sMgmntIntCIDR = map[k8snet.IPFamily]*net.IPNet{}

		t.netLink = fakenetlink.New()
		netlinkAPI.NewFunc = func() netlinkAPI.Interface {
			return t.netLink
		}
	})

	JustBeforeEach(func() {
		t.createNode()

		link := &netlink.GenericLink{
			LinkAttrs: netlink.LinkAttrs{
				Index: OVNK8sMgmntIntIndex,
				Name:  ovn.OVNK8sMgmntIntfName,
			},
		}

		t.netLink.SetLinkIndex(ovn.OVNK8sMgmntIntfName, link.Index)
		Expect(t.netLink.LinkAdd(link)).To(Succeed())

		cidrs := []*net.IPNet{toIPNet("128.1.20.2/24"), toIPNet("a000:200::/64")}
		for _, c := range cidrs {
			Expect(t.netLink.AddrAdd(link, &netlink.Addr{
				IPNet: c,
			})).To(Succeed())
		}

		t.OVNK8sMgmntIntCIDR[k8snet.IPv4] = cidrs[0]
		t.OVNK8sMgmntIntCIDR[k8snet.IPv6] = cidrs[1]
	})

	return t
}

func (t *testDriver) createNode() {
	t.node = createNode(t.k8sClient, t.transitSwitchIP[k8snet.IPv4], t.transitSwitchIP[k8snet.IPv6])
}

func (t *testDriver) awaitOVNKNodeAnnotationContaining(expected ...string) {
	if expected == nil {
		expected = []string{}
	}

	Eventually(func(g Gomega) {
		node, err := t.k8sClient.CoreV1().Nodes().Get(context.TODO(), nodeutil.GetLocalNodeName(), metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		var actual []string

		if node.Annotations[ovn.OVNKSNATExcludeSubnetsAnnotation] != "" {
			err = json.Unmarshal([]byte(node.Annotations[ovn.OVNKSNATExcludeSubnetsAnnotation]), &actual)
			Expect(err).NotTo(HaveOccurred())
		}

		sort.Strings(expected)
		sort.Strings(actual)

		g.Expect(actual).To(Equal(expected))
	}).Within(3 * time.Second).Should(Succeed())
}

func (t *testDriver) createEndpoint(subnets ...string) *submV1.Endpoint {
	return t.CreateEndpoint(eventtesting.NewEndpoint(remoteClusterID, "host", subnets...))
}

func createNode(k8sClient kubernetes.Interface, transitSwitchIP ...string) *corev1.Node {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}

	tsIPAnnotation := toTransitSwitchIPAnnotation(transitSwitchIP...)
	if tsIPAnnotation != "" {
		node.Annotations = map[string]string{constants.OvnTransitSwitchIPAnnotation: tsIPAnnotation}
	}

	_, err := k8sClient.CoreV1().Nodes().Create(context.Background(), node, metav1.CreateOptions{})
	Expect(err).To(Succeed())

	os.Setenv("NODE_NAME", node.Name)

	nodeutil.PollTimeout = 100 * time.Millisecond
	nodeutil.PollInterval = 10 * time.Millisecond

	return node
}

func toTransitSwitchIPAnnotation(ips ...string) string {
	data := map[string]string{}

	for _, ip := range ips {
		if k8snet.IsIPv4String(ip) {
			data["ipv4"] = ip + "/24"
		} else if k8snet.IsIPv6String(ip) {
			data["ipv6"] = ip + "/64"
		}
	}

	if len(data) == 0 {
		return ""
	}

	bytes, err := json.Marshal(data)
	Expect(err).To(Succeed())

	return string(bytes)
}

func toIPNet(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	utilruntime.Must(err)

	return n
}
