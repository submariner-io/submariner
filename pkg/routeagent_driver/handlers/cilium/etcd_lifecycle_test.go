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

package cilium //nolint:testpackage // Tests exercise unexported embedded-etcd implementation details.

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	eventtesting "github.com/submariner-io/submariner/pkg/event/testing"
	fakek8s "k8s.io/client-go/kubernetes/fake"
)

var _ = Describe("etcd store lifecycle", func() {
	It("should release listen ports and remove owned temp data dir on Close", func(ctx context.Context) {
		clientPort := freeTCPPort()
		peerPort := freeTCPPort()
		clientURL := fmt.Sprintf("http://127.0.0.1:%d", clientPort)
		peerURL := fmt.Sprintf("http://127.0.0.1:%d", peerPort)

		store, err := startEtcdStore(ctx, &EtcdStoreConfig{
			// empty DataDir → MkdirTemp + remove on Close
			ListenClientURL:    clientURL,
			AdvertiseClientURL: clientURL,
			ListenPeerURL:      peerURL,
			AdvertisePeerURL:   peerURL,
			Name:               "lifecycle-cm",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(store.dataDir).NotTo(BeEmpty())
		Expect(store.removeDataDir).To(BeTrue())

		dataDir := store.dataDir
		_, err = os.Stat(dataDir)
		Expect(err).NotTo(HaveOccurred())

		Expect(store.Bootstrap(ctx, "submariner", 255)).To(Succeed())
		Expect(store.UpsertRoute(ctx, "10.151.0.0/16", "10.0.0.2", 255)).To(Succeed())

		Expect(store.Close()).To(Succeed())
		Expect(store.Close()).To(Succeed()) // idempotent

		Eventually(func() bool {
			_, err := os.Stat(dataDir)
			return os.IsNotExist(err)
		}).Should(BeTrue())

		// Ports must be reusable after Close (no listener leak).
		Eventually(func() error {
			return canListen(clientPort)
		}).WithTimeout(5 * time.Second).Should(Succeed())
		Expect(canListen(peerPort)).To(Succeed())
	})

	It("should fail cleanly when the listen port is already taken", func(ctx context.Context) {
		clientPort := freeTCPPort()
		peerPort := freeTCPPort()

		blocker, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", clientPort))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = blocker.Close() })

		_, err = startEtcdStore(ctx, &EtcdStoreConfig{
			ListenClientURL: fmt.Sprintf("http://127.0.0.1:%d", clientPort),
			ListenPeerURL:   fmt.Sprintf("http://127.0.0.1:%d", peerPort),
			Name:            "port-conflict-cm",
		})
		Expect(err).To(HaveOccurred())

		// Peer port from the failed attempt must still be free.
		Expect(canListen(peerPort)).To(Succeed())
	})
})

var _ = Describe("ClusterMesh publisher etcd lifecycle", func() {
	It("should start embedded etcd on Init and release it on Stop", func(ctx context.Context) {
		clientPort := freeTCPPort()
		peerPort := freeTCPPort()
		clientURL := fmt.Sprintf("http://127.0.0.1:%d", clientPort)
		peerURL := fmt.Sprintf("http://127.0.0.1:%d", peerPort)

		k8sClient := fakek8s.NewClientset(
			newNodeWithIP("node-local", "10.0.0.1"),
			newNodeWithIP("node-gw", "10.0.0.2"),
		)

		h := NewClusterMeshPublisher(k8sClient, &PublisherConfig{
			LocalNodeIP:        "10.0.0.1",
			ListenClientURL:    clientURL,
			AdvertiseClientURL: clientURL,
			ListenPeerURL:      peerURL,
			AdvertisePeerURL:   peerURL,
		})
		Expect(h).NotTo(BeNil())

		pub := h.(*clusterMeshPublisher)
		pub.SetState(&eventtesting.TestHandlerState{})

		Expect(pub.Init(ctx)).To(Succeed())
		Expect(pub.store).NotTo(BeNil())

		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", clientPort), time.Second)
		Expect(err).NotTo(HaveOccurred())

		_ = conn.Close()

		Expect(pub.Stop(ctx)).To(Succeed())
		Expect(pub.Stop(ctx)).To(Succeed()) // idempotent
		Expect(pub.store).To(BeNil())

		Eventually(func() error {
			return canListen(clientPort)
		}).WithTimeout(5 * time.Second).Should(Succeed())
	})

	It("should close the store if Init reconcile fails", func(ctx context.Context) {
		failing := &failingBootstrapStore{inner: newMemoryStore()}

		h := NewClusterMeshPublisher(fakek8s.NewClientset(
			newNodeWithIP("node-local", "10.0.0.1"),
			newNodeWithIP("node-gw", "10.0.0.2"),
		), &PublisherConfig{
			LocalNodeIP: "10.0.0.1",
			Store:       failing,
		})
		pub := h.(*clusterMeshPublisher)
		pub.SetState(&eventtesting.TestHandlerState{})

		Expect(pub.Init(ctx)).NotTo(Succeed())
		Expect(failing.closed).To(BeTrue())
		Expect(pub.store).To(BeNil())
	})
})

type failingBootstrapStore struct {
	inner  *memoryStore
	closed bool
}

func (s *failingBootstrapStore) Bootstrap(context.Context, string, uint32) error {
	return errors.New("bootstrap failed")
}

func (s *failingBootstrapStore) UpsertRoute(ctx context.Context, cidrStr, hostIP string, clusterID uint32) error {
	return s.inner.UpsertRoute(ctx, cidrStr, hostIP, clusterID)
}

func (s *failingBootstrapStore) DeleteRoute(ctx context.Context, cidrStr string) error {
	return s.inner.DeleteRoute(ctx, cidrStr)
}

func (s *failingBootstrapStore) DeleteClusterConfig(ctx context.Context, remoteName string) error {
	return s.inner.DeleteClusterConfig(ctx, remoteName)
}

func (s *failingBootstrapStore) TouchHeartbeat(ctx context.Context) error {
	return s.inner.TouchHeartbeat(ctx)
}

func (s *failingBootstrapStore) Close() error {
	s.closed = true
	return s.inner.Close()
}

func canListen(port int) error {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}

	return l.Close()
}
