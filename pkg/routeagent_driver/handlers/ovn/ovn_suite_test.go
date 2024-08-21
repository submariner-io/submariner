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
	"net"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	fakesubm "github.com/submariner-io/submariner/pkg/client/clientset/versioned/fake"
	"github.com/submariner-io/submariner/pkg/event"
	eventtesting "github.com/submariner-io/submariner/pkg/event/testing"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	fakenetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	fakePF "github.com/submariner-io/submariner/pkg/packetfilter/fake"
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
)

const (
	OVNK8sMgmntIntIndex = 99
)

var OVNK8sMgmntIntCIDR = toIPNet("128.1.20.2/24")

func init() {
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
	submClient      *fakesubm.Clientset
	k8sClient       *fakek8s.Clientset
	dynClient       *fakedynamic.FakeDynamicClient
	netLink         *fakenetlink.NetLink
	pFilter         *fakePF.PacketFilter
	handler         event.Handler
	transitSwitchIP string
	mgmntIntfIP     string
}

func newTestDriver() *testDriver {
	t := &testDriver{
		ControllerSupport: eventtesting.NewControllerSupport(),
	}

	BeforeEach(func() {
		t.transitSwitchIP = "190.1.2.0"
		t.submClient = fakesubm.NewSimpleClientset()
		t.k8sClient = fakek8s.NewClientset()
		t.dynClient = fakedynamic.NewSimpleDynamicClient(scheme.Scheme)

		t.netLink = fakenetlink.New()
		netlinkAPI.NewFunc = func() netlinkAPI.Interface {
			return t.netLink
		}
		t.pFilter = fakePF.New()

		link := &netlink.GenericLink{
			LinkAttrs: netlink.LinkAttrs{
				Index: OVNK8sMgmntIntIndex,
				Name:  ovn.OVNK8sMgmntIntfName,
			},
		}

		t.netLink.SetLinkIndex(ovn.OVNK8sMgmntIntfName, link.Index)
		Expect(t.netLink.LinkAdd(link)).To(Succeed())

		addr := &netlink.Addr{
			IPNet: OVNK8sMgmntIntCIDR,
		}
		Expect(t.netLink.AddrAdd(link, addr)).To(Succeed())

		t.mgmntIntfIP = addr.IPNet.IP.String()
	})

	return t
}

func (t *testDriver) Start(handler event.Handler) {
	node := t.createNode()
	t.handler = handler
	t.ControllerSupport.Start(handler)
	t.CreateNode(node)
}

func (t *testDriver) createNode() *corev1.Node {
	return createNode(t.k8sClient, t.transitSwitchIP)
}

func createNode(k8sClient kubernetes.Interface, transitSwitchIP string) *corev1.Node {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}

	if transitSwitchIP != "" {
		node.Annotations = map[string]string{constants.OvnTransitSwitchIPAnnotation: toTransitSwitchIPAnnotation(transitSwitchIP)}
	}

	_, err := k8sClient.CoreV1().Nodes().Create(context.Background(), node, metav1.CreateOptions{})
	Expect(err).To(Succeed())

	os.Setenv("NODE_NAME", node.Name)

	return node
}

func toTransitSwitchIPAnnotation(ip string) string {
	data := map[string]string{"ipv4": ip + "/24"}
	bytes, err := json.Marshal(data)
	Expect(err).To(Succeed())

	return string(bytes)
}

func toIPNet(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	utilruntime.Must(err)

	return n
}
