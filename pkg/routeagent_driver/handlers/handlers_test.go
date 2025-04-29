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

package handlers_test

import (
	"context"
	"net"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	libovsdbclient "github.com/ovn-org/libovsdb/client"
	"github.com/ovn-org/libovsdb/model"
	calicocs "github.com/projectcalico/api/pkg/client/clientset_generated/clientset"
	calicocsfake "github.com/projectcalico/api/pkg/client/clientset_generated/clientset/fake"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	fakesubm "github.com/submariner-io/submariner/pkg/client/clientset/versioned/fake"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/event/testing"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	fakenetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	fakePF "github.com/submariner-io/submariner/pkg/packetfilter/fake"
	"github.com/submariner-io/submariner/pkg/pinger"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/calico"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/healthchecker"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/kubeproxy"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/mtu"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/ovn"
	fakeovn "github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/ovn/fake"
	"github.com/vishvananda/netlink"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	fakek8s "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	k8snet "k8s.io/utils/net"
)

var _ = Describe("", func() {
	ctx := context.TODO()
	localClusterCIDRs := []string{"10.1.0.0/24"}
	localServiceCIDRs := []string{"181.0.1.0/24"}

	var (
		k8sClient  *fakek8s.Clientset
		submClient *fakesubm.Clientset
		dynClient  *fakedynamic.FakeDynamicClient
	)

	BeforeEach(func() {
		_ = fakePF.New()

		netLink := fakenetlink.New()
		netlinkAPI.NewFunc = func() netlinkAPI.Interface {
			return netLink
		}

		calico.NewClient = func(_ *rest.Config) (calicocs.Interface, error) {
			return calicocsfake.NewSimpleClientset(), nil
		}

		k8sClient = fakek8s.NewClientset()
		submClient = fakesubm.NewClientset()
		dynClient = fakedynamic.NewSimpleDynamicClient(scheme.Scheme)

		setupOVN(k8sClient, netLink)

		cni.HostInterfaces = func() ([]cni.HostInterface, error) {
			return []cni.HostInterface{{
				Name: "veth0",
				Addr: localClusterCIDRs[0],
			}}, nil
		}

		netLink.SetupDefaultGateway(k8snet.IPv4, net.Interface{
			Name: "gw-intf",
		}, &net.IPNet{IP: net.ParseIP("1.2.3.4"), Mask: net.CIDRMask(8, 32)})
	})

	Specify("All Route Agent handlers should successfully instantiate and initialize", func() {
		kubeproxyHandler := kubeproxy.NewSyncHandler(k8snet.IPv4, localClusterCIDRs, localServiceCIDRs)
		Expect(kubeproxyHandler.Init(ctx)).To(Succeed())

		ovnHandler := ovn.NewHandler(&ovn.HandlerConfig{
			Namespace:   testing.Namespace,
			ClusterCIDR: localClusterCIDRs,
			ServiceCIDR: localServiceCIDRs,
			SubmClient:  submClient,
			K8sClient:   k8sClient,
			DynClient:   dynClient,
			WatcherConfig: &watcher.Config{
				RestMapper: test.GetRESTMapperFor(&submarinerv1.GatewayRoute{}, &submarinerv1.NonGatewayRoute{}),
				Client:     dynClient,
			},
			NewOVSDBClient: func(_ model.ClientDBModel, _ ...libovsdbclient.Option) (libovsdbclient.Client, error) {
				return fakeovn.NewOVSDBClient(), nil
			},
			TransitSwitchIP: ovn.NewTransitSwitchIP(),
		})
		Expect(ovnHandler.Init(ctx)).To(Succeed())

		gwRouteHandler := ovn.NewGatewayRouteHandler(submClient)
		Expect(gwRouteHandler.Init(ctx)).To(Succeed())

		ngwRouteHandler := ovn.NewNonGatewayRouteHandler(submClient, ovn.NewTransitSwitchIP())
		Expect(ngwRouteHandler.Init(ctx)).To(Succeed())

		mtuHandler := mtu.NewHandler(k8snet.IPv4, localClusterCIDRs, false, 0)
		Expect(mtuHandler.Init(ctx)).To(Succeed())

		calicoHandler := calico.NewCalicoIPPoolHandler(nil, testing.Namespace, k8sClient)
		Expect(calicoHandler.Init(ctx)).To(Succeed())

		healthCheckerHandler := healthchecker.New(&healthchecker.Config{
			ControllerConfig: pinger.ControllerConfig{
				SupportedIPFamilies: []k8snet.IPFamily{k8snet.IPv4},
			},
			HealthCheckerEnabled:     false,
			RouteAgentUpdateInterval: time.Hour,
		}, submClient.SubmarinerV1().RouteAgents(testing.Namespace), "v1", "test-node")
		Expect(healthCheckerHandler.Init(ctx)).To(Succeed())
	})
})

func setupOVN(k8sClient kubernetes.Interface, netLink *fakenetlink.NetLink) {
	_, err := k8sClient.CoreV1().Pods(testing.Namespace).Create(context.Background(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "ovn-pod",
			Labels: map[string]string{"app": "ovnkube-node"},
		},
	}, metav1.CreateOptions{})
	Expect(err).To(Succeed())

	node, err := k8sClient.CoreV1().Nodes().Create(context.Background(), &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
	}, metav1.CreateOptions{})
	Expect(err).To(Succeed())

	os.Setenv("NODE_NAME", node.Name)

	link := &netlink.GenericLink{
		LinkAttrs: netlink.LinkAttrs{
			Index: 99,
			Name:  ovn.OVNK8sMgmntIntfName,
		},
	}

	netLink.SetLinkIndex(ovn.OVNK8sMgmntIntfName, link.Index)
	Expect(netLink.LinkAdd(link)).To(Succeed())

	var ovnK8sMgmntIntCIDR *net.IPNet
	_, ovnK8sMgmntIntCIDR, _ = net.ParseCIDR("128.1.20.2/24")

	addr := &netlink.Addr{
		IPNet: ovnK8sMgmntIntCIDR,
	}
	Expect(netLink.AddrAdd(link, addr)).To(Succeed())
}
