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

package cilium

import (
	"context"
	"encoding/json"
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/event"
	eventtesting "github.com/submariner-io/submariner/pkg/event/testing"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/cilium/fake"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakek8s "k8s.io/client-go/kubernetes/fake"
)

var _ = Describe("ClusterMesh publisher", func() {
	const (
		localIP    = "10.0.0.1"
		gatewayIP  = "10.0.0.2"
		otherIP    = "10.0.0.3"
		remoteCIDR = "10.151.0.0/16"
	)

	var (
		k8sClient       *fakek8s.Clientset
		etcdClient      *fake.EtcdClient
		handler         event.Handler
		support         *eventtesting.ControllerSupport
		preferredHostIP string
	)

	BeforeEach(func() {
		preferredHostIP = ""
		k8sClient = fakek8s.NewClientset(
			newNodeWithIP("node-local", localIP),
			newNodeWithIP("node-gw", gatewayIP),
			newNodeWithIP("node-other", otherIP),
		)
		etcdClient = fake.NewEtcdClient()
		support = eventtesting.NewControllerSupport()
	})

	JustBeforeEach(func(ctx context.Context) {
		h := NewClusterMeshPublisher(k8sClient, &PublisherConfig{
			LocalNodeIP:     localIP,
			PreferredHostIP: preferredHostIP,
			RemoteName:      "submariner",
			ClusterID:       255,
			EtcdClient:      etcdClient,
		})
		Expect(h).NotTo(BeNil())
		handler = h
		support.Start(ctx, handler)
	})

	It("should report the cilium network plugin", func() {
		Expect(handler.GetNetworkPlugins()).To(Equal([]string{cni.Cilium}))
		Expect(handler.GetName()).To(Equal("Cilium ClusterMesh publisher"))
	})

	It("should write cilium/.heartbeat on start", func() {
		Eventually(func() string { return string(etcdClient.Value(cmHeartbeatKey)) }).ShouldNot(BeEmpty())

		ts, err := time.Parse(time.RFC3339, string(etcdClient.Value(cmHeartbeatKey)))
		Expect(err).NotTo(HaveOccurred())
		Expect(ts).To(BeTemporally("~", time.Now().UTC(), 5*time.Second))
	})

	When("a remote Endpoint is created", func() {
		It("should publish CIDR routes with HostIP != local", func(ctx context.Context) {
			support.CreateEndpoint(ctx, eventtesting.NewEndpoint("west", "host", remoteCIDR))

			Eventually(func() int { return routeKeyCount(etcdClient) }).Should(Equal(1))

			raw := etcdClient.Value(ipIdentityKey(remoteCIDR))
			Expect(raw).NotTo(BeNil())

			var pair ipIdentityPair
			Expect(json.Unmarshal(raw, &pair)).To(Succeed())
			Expect(pair.HostIP.String()).NotTo(Equal(localIP))
			Expect(pair.HostIP.String()).To(Equal(gatewayIP))
			Expect(pair.ID).To(Equal(uint32(16712680))) // (255 << 16) | 1000

			Expect(etcdClient.HasKey(clusterConfigKey("submariner"))).To(BeTrue())
		})

		It("should prefer LocalEndpoint PrivateIP as HostIP", func(ctx context.Context) {
			localEP := eventtesting.NewEndpoint("east", "local-gw", "10.0.0.0/16")
			localEP.Spec.SetPrivateIP(otherIP)
			Expect(handler.LocalEndpointCreated(localEP)).To(Succeed())

			support.CreateEndpoint(ctx, eventtesting.NewEndpoint("west", "host", remoteCIDR))

			Eventually(func() string {
				return routeHostIP(etcdClient, remoteCIDR)
			}).Should(Equal(otherIP))
		})

		When("PreferredHostIP is configured", func() {
			BeforeEach(func() {
				preferredHostIP = "10.0.0.9"
			})

			It("should honor PreferredHostIP over gateway and node list", func(ctx context.Context) {
				support.CreateEndpoint(ctx, eventtesting.NewEndpoint("west", "host", remoteCIDR))

				Eventually(func() string {
					return routeHostIP(etcdClient, remoteCIDR)
				}).Should(Equal("10.0.0.9"))
			})
		})

		It("should delete routes when the remote Endpoint is removed", func(ctx context.Context) {
			ep := eventtesting.NewEndpoint("west", "host", remoteCIDR)
			support.CreateEndpoint(ctx, ep)
			Eventually(func() int { return routeKeyCount(etcdClient) }).Should(Equal(1))

			support.DeleteEndpoint(ctx, ep.Name)
			Eventually(func() int { return routeKeyCount(etcdClient) }).Should(Equal(0))
		})

		It("should republish when HostIP changes after LocalEndpointUpdated", func(ctx context.Context) {
			support.CreateEndpoint(ctx, eventtesting.NewEndpoint("west", "host", remoteCIDR))
			Eventually(func() string { return routeHostIP(etcdClient, remoteCIDR) }).Should(Equal(gatewayIP))

			localEP := eventtesting.NewEndpoint("east", "local-gw", "10.0.0.0/16")
			localEP.Spec.SetPrivateIP(otherIP)
			Expect(handler.LocalEndpointUpdated(localEP)).To(Succeed())

			Eventually(func() string { return routeHostIP(etcdClient, remoteCIDR) }).Should(Equal(otherIP))
			Expect(routeKeyCount(etcdClient)).To(Equal(1))
		})

		It("should sync route keys when remote Endpoint subnets change", func(ctx context.Context) {
			const (
				cidrA = "10.151.0.0/16"
				cidrB = "10.152.0.0/16"
				cidrC = "10.153.0.0/16"
			)

			ep := support.CreateEndpoint(ctx, eventtesting.NewEndpoint("west", "host", cidrA))
			Eventually(func() int { return routeKeyCount(etcdClient) }).Should(Equal(1))
			Expect(etcdClient.Value(ipIdentityKey(cidrA))).NotTo(BeNil())

			ep.Spec.Subnets = []string{cidrB, cidrC}
			support.UpdateEndpoint(ctx, ep)

			Eventually(func() int { return routeKeyCount(etcdClient) }).Should(Equal(2))
			Expect(etcdClient.Value(ipIdentityKey(cidrA))).To(BeNil())
			Expect(etcdClient.Value(ipIdentityKey(cidrB))).NotTo(BeNil())
			Expect(etcdClient.Value(ipIdentityKey(cidrC))).NotTo(BeNil())
		})

		It("should compact overlapping CIDRs across remote Endpoints", func(ctx context.Context) {
			const (
				shared = "10.151.0.0/16"
				extra  = "10.152.0.0/16"
			)

			support.CreateEndpoint(ctx, eventtesting.NewEndpoint("west", "host-a", shared))
			Eventually(func() int { return routeKeyCount(etcdClient) }).Should(Equal(1))

			support.CreateEndpoint(ctx, eventtesting.NewEndpoint("north", "host-b", shared, extra))
			Eventually(func() int { return routeKeyCount(etcdClient) }).Should(Equal(2))
			Expect(etcdClient.Value(ipIdentityKey(shared))).NotTo(BeNil())
			Expect(etcdClient.Value(ipIdentityKey(extra))).NotTo(BeNil())
		})

		It("should delete published keys on Stop before closing the store", func(ctx context.Context) {
			support.CreateEndpoint(ctx, eventtesting.NewEndpoint("west", "host", remoteCIDR))
			Eventually(func() int { return routeKeyCount(etcdClient) }).Should(Equal(1))
			Expect(etcdClient.HasKey(clusterConfigKey("submariner"))).To(BeTrue())

			Expect(handler.Stop(ctx)).To(Succeed())
			Expect(handler.Stop(ctx)).To(Succeed()) // sync.Once — idempotent
			Expect(routeKeyCount(etcdClient)).To(Equal(0))
			Expect(etcdClient.HasKey(clusterConfigKey("submariner"))).To(BeFalse())
			Expect(etcdClient.Closed()).To(BeTrue())
		})
	})

	When("no HostIP candidate exists", func() {
		BeforeEach(func() {
			k8sClient = fakek8s.NewClientset(newNodeWithIP("node-local", localIP))
		})

		It("should skip publishing without failing reconcile", func(ctx context.Context) {
			Eventually(func() string { return string(etcdClient.Value(cmHeartbeatKey)) }).ShouldNot(BeEmpty())

			support.CreateEndpoint(ctx, eventtesting.NewEndpoint("west", "host", remoteCIDR))
			Consistently(func() int { return routeKeyCount(etcdClient) }).WithTimeout(500 * time.Millisecond).
				Should(Equal(0))
			Expect(etcdClient.HasKey(clusterConfigKey("submariner"))).To(BeFalse())
		})
	})
})

var _ = Describe("ClusterMesh publisher Init", func() {
	It("should reject empty LocalNodeIP", func(ctx context.Context) {
		h := NewClusterMeshPublisher(fakek8s.NewClientset(), &PublisherConfig{
			EtcdClient: fake.NewEtcdClient(),
		})
		pub := h.(*clusterMeshPublisher)
		pub.SetState(&eventtesting.TestHandlerState{})

		Expect(pub.Init(ctx)).To(MatchError(ContainSubstring("LocalNodeIP")))
	})

	It("should close the store if Init reconcile fails", func(ctx context.Context) {
		etcdClient := fake.NewEtcdClient()
		etcdClient.SetPutError(errors.New("bootstrap failed"))

		h := NewClusterMeshPublisher(fakek8s.NewClientset(
			newNodeWithIP("node-local", "10.0.0.1"),
			newNodeWithIP("node-gw", "10.0.0.2"),
		), &PublisherConfig{
			LocalNodeIP: "10.0.0.1",
			EtcdClient:  etcdClient,
		})
		pub := h.(*clusterMeshPublisher)
		pub.SetState(&eventtesting.TestHandlerState{})

		Expect(pub.Init(ctx)).NotTo(Succeed())
		Expect(etcdClient.Closed()).To(BeTrue())
	})
})

var _ = Describe("ClusterMesh publisher HostIP != self", func() {
	It("should never publish HostIP equal to the local node IP", func(ctx context.Context) {
		const (
			localIP    = "10.0.0.1"
			gatewayIP  = "10.0.0.2"
			remoteCIDR = "10.151.0.0/16"
		)

		k8sClient := fakek8s.NewClientset(
			newNodeWithIP("node-local", localIP),
			newNodeWithIP("node-gw", gatewayIP),
		)
		etcdClient := fake.NewEtcdClient()
		support := eventtesting.NewControllerSupport()

		h := NewClusterMeshPublisher(k8sClient, &PublisherConfig{
			LocalNodeIP: localIP,
			EtcdClient:  etcdClient,
		})
		support.Start(ctx, h)

		support.CreateEndpoint(ctx, eventtesting.NewEndpoint("west", "host", remoteCIDR))
		Eventually(func() int { return routeKeyCount(etcdClient) }).Should(Equal(1))

		raw := etcdClient.Value(ipIdentityKey(remoteCIDR))
		Expect(raw).NotTo(BeNil())

		var pair ipIdentityPair
		Expect(json.Unmarshal(raw, &pair)).To(Succeed())
		Expect(pair.HostIP.Equal(net.ParseIP(localIP))).To(BeFalse())
		Expect(pair.HostIP.String()).To(Equal(gatewayIP))
	})
})

func routeKeyCount(c *fake.EtcdClient) int {
	return c.KeyCountWithPrefix(cmIPStatePrefix + "/")
}

func routeHostIP(c *fake.EtcdClient, cidrStr string) string {
	raw := c.Value(ipIdentityKey(cidrStr))
	if raw == nil {
		return ""
	}

	var pair ipIdentityPair
	if err := json.Unmarshal(raw, &pair); err != nil {
		return ""
	}

	return pair.HostIP.String()
}

func newNodeWithIP(name, ip string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: ip},
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
}
