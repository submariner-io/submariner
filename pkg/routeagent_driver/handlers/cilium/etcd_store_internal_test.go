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
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/cilium/fake"
)

var _ = Describe("etcdStore", func() {
	It("should bootstrap and upsert/delete routes with correct data", func(ctx context.Context) {
		etcdClient := fake.NewEtcdClient()
		store := newEtcdStoreWithClient(etcdClient)

		Expect(store.Bootstrap(ctx, "submariner", 255)).To(Succeed())
		Expect(store.UpsertRoute(ctx, "10.151.0.0/16", "10.0.0.2", 255)).To(Succeed())
		Expect(store.TouchHeartbeat(ctx)).To(Succeed())

		Expect(etcdClient.HasKey(clusterConfigKey("submariner"))).To(BeTrue())

		raw := etcdClient.Value(ipIdentityKey("10.151.0.0/16"))
		Expect(raw).NotTo(BeNil())

		var pair ipIdentityPair
		Expect(json.Unmarshal(raw, &pair)).To(Succeed())
		Expect(pair.HostIP.String()).To(Equal("10.0.0.2"))

		hb := etcdClient.Value(cmHeartbeatKey)
		Expect(hb).NotTo(BeNil())
		_, err := time.Parse(time.RFC3339, string(hb))
		Expect(err).NotTo(HaveOccurred())

		Expect(store.DeleteRoute(ctx, "10.151.0.0/16")).To(Succeed())
		Expect(etcdClient.Value(ipIdentityKey("10.151.0.0/16"))).To(BeNil())
	})

	It("should propagate Put errors from the client", func(ctx context.Context) {
		etcdClient := fake.NewEtcdClient()
		etcdClient.SetPutError(errors.New("put failed"))
		store := newEtcdStoreWithClient(etcdClient)

		Expect(store.Bootstrap(ctx, "submariner", 255)).To(MatchError(ContainSubstring("put failed")))
	})

	It("should release listen ports and remove owned temp data dir on Close", func(ctx context.Context) {
		clientPort := freeTCPPort()
		peerPort := freeTCPPort()
		clientURL := fmt.Sprintf("http://127.0.0.1:%d", clientPort)
		peerURL := fmt.Sprintf("http://127.0.0.1:%d", peerPort)

		store, err := startEtcdStore(ctx, &EtcdStoreConfig{
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
		Expect(store.Close()).To(Succeed()) // sync.Once — idempotent

		Eventually(func() bool {
			_, err := os.Stat(dataDir)
			return os.IsNotExist(err)
		}).Should(BeTrue())

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

		Expect(canListen(peerPort)).To(Succeed())
	})

	It("should bootstrap against a real embedded etcd", func(ctx context.Context) {
		dir := GinkgoT().TempDir()
		clientPort := freeTCPPort()
		peerPort := freeTCPPort()

		store, err := startEtcdStore(ctx, &EtcdStoreConfig{
			DataDir:         filepath.Join(dir, "etcd"),
			ListenClientURL: "http://127.0.0.1:" + strconv.Itoa(clientPort),
			ListenPeerURL:   "http://127.0.0.1:" + strconv.Itoa(peerPort),
			Name:            "test-cm",
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(store.Close()).To(Succeed())
		})

		Expect(store.Bootstrap(ctx, "submariner", 255)).To(Succeed())
		Expect(store.UpsertRoute(ctx, "10.151.0.0/16", "10.0.0.2", 255)).To(Succeed())
		Expect(store.TouchHeartbeat(ctx)).To(Succeed())

		resp, err := store.client.Get(ctx, ipIdentityKey("10.151.0.0/16"))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Kvs).To(HaveLen(1))

		var pair ipIdentityPair
		Expect(json.Unmarshal(resp.Kvs[0].Value, &pair)).To(Succeed())
		Expect(pair.HostIP.String()).To(Equal("10.0.0.2"))
	})
})

func freeTCPPort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())

	defer l.Close()

	return l.Addr().(*net.TCPAddr).Port
}

func canListen(port int) error {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}

	return l.Close()
}
