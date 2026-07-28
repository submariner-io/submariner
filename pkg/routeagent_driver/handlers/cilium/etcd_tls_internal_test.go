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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	eventtesting "github.com/submariner-io/submariner/pkg/event/testing"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	fakek8s "k8s.io/client-go/kubernetes/fake"
)

var _ = Describe("embedded etcd TLS with client cert auth", func() {
	It("should reject clients that do not present a certificate", func(ctx context.Context) {
		dir := GinkgoT().TempDir()
		paths := writeTestTLSBundle(dir)

		clientPort := freeTCPPort()
		peerPort := freeTCPPort()
		clientURL := fmt.Sprintf("https://127.0.0.1:%d", clientPort)
		peerURL := fmt.Sprintf("http://127.0.0.1:%d", peerPort)

		store, err := startEtcdStore(ctx, &EtcdStoreConfig{
			DataDir:            filepath.Join(dir, "etcd"),
			ListenClientURL:    clientURL,
			AdvertiseClientURL: clientURL,
			ListenPeerURL:      peerURL,
			AdvertisePeerURL:   peerURL,
			Name:               "tls-cm-reject-anon",
			CertFile:           paths.serverCert,
			KeyFile:            paths.serverKey,
			CAFile:             paths.caCert,
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(store.Close()).To(Succeed())
		})

		// Trust the CA (so server cert verifies) but omit a client certificate.
		tlsInfo := transport.TLSInfo{
			TrustedCAFile: paths.caCert,
		}
		tlsCfg, err := tlsInfo.ClientConfig()
		Expect(err).NotTo(HaveOccurred())

		cli, err := clientv3.New(clientv3.Config{
			Endpoints:   []string{clientURL},
			DialTimeout: 2 * time.Second,
			TLS:         tlsCfg,
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = cli.Close()
		})

		getCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()

		_, err = cli.Get(getCtx, clusterConfigKey("submariner"))
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("ClusterMesh publisher TLS with client cert auth", func() {
	const (
		localIP    = "10.0.0.1"
		gatewayIP  = "10.0.0.2"
		remoteCIDR = "10.151.0.0/16"
	)

	It("should publish routes and heartbeat readable by a Cilium-shaped client", func(ctx context.Context) {
		dir := GinkgoT().TempDir()
		paths := writeTestTLSBundle(dir)

		clientPort := freeTCPPort()
		peerPort := freeTCPPort()
		clientURL := fmt.Sprintf("https://127.0.0.1:%d", clientPort)
		peerURL := fmt.Sprintf("http://127.0.0.1:%d", peerPort)

		support := eventtesting.NewControllerSupport()
		h := NewClusterMeshPublisher(fakek8s.NewClientset(
			newNodeWithIP("node-local", localIP),
			newNodeWithIP("node-gw", gatewayIP),
		), &PublisherConfig{
			LocalNodeIP:        localIP,
			RemoteName:         "submariner",
			ClusterID:          255,
			ListenClientURL:    clientURL,
			AdvertiseClientURL: clientURL,
			ListenPeerURL:      peerURL,
			AdvertisePeerURL:   peerURL,
			DataDir:            filepath.Join(dir, "etcd"),
			CertFile:           paths.serverCert,
			KeyFile:            paths.serverKey,
			CAFile:             paths.caCert,
		})
		support.Start(ctx, h)

		cli := newCiliumShapedEtcdClient(clientURL, &paths)
		DeferCleanup(func() {
			_ = cli.Close()
		})

		Eventually(func(g Gomega) {
			hb, err := cli.Get(ctx, cmHeartbeatKey)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(hb.Kvs).To(HaveLen(1))
			_, err = time.Parse(time.RFC3339, string(hb.Kvs[0].Value))
			g.Expect(err).NotTo(HaveOccurred())
		}).WithTimeout(5 * time.Second).Should(Succeed())

		ep := eventtesting.NewEndpoint("west", "host", remoteCIDR)
		support.CreateEndpoint(ctx, ep)

		Eventually(func(g Gomega) {
			cfg, err := cli.Get(ctx, clusterConfigKey("submariner"))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(cfg.Kvs).To(HaveLen(1))

			resp, err := cli.Get(ctx, ipIdentityKey(remoteCIDR))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.Kvs).To(HaveLen(1))

			var pair ipIdentityPair
			g.Expect(json.Unmarshal(resp.Kvs[0].Value, &pair)).To(Succeed())
			g.Expect(pair.HostIP.String()).To(Equal(gatewayIP))
			g.Expect(pair.HostIP.String()).NotTo(Equal(localIP))
		}).WithTimeout(5 * time.Second).Should(Succeed())

		support.DeleteEndpoint(ctx, ep.Name)
		Eventually(func(g Gomega) {
			resp, err := cli.Get(ctx, ipIdentityKey(remoteCIDR))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.Kvs).To(BeEmpty())
		}).WithTimeout(5 * time.Second).Should(Succeed())

		Expect(h.Stop(ctx)).To(Succeed())

		Eventually(func() error {
			return canListen(clientPort)
		}).WithTimeout(5 * time.Second).Should(Succeed())
	})
})

func newCiliumShapedEtcdClient(endpoint string, paths *testTLSPaths) *clientv3.Client {
	GinkgoHelper()

	tlsInfo := transport.TLSInfo{
		CertFile:      paths.clientCert,
		KeyFile:       paths.clientKey,
		TrustedCAFile: paths.caCert,
	}
	tlsCfg, err := tlsInfo.ClientConfig()
	Expect(err).NotTo(HaveOccurred())

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: 5 * time.Second,
		TLS:         tlsCfg,
	})
	Expect(err).NotTo(HaveOccurred())

	return cli
}

type testTLSPaths struct {
	caCert     string
	serverCert string
	serverKey  string
	clientCert string
	clientKey  string
}

// writeTestTLSBundle mirrors the operator ciliumcm.GenerateBundle shape used in
// production (CA + server SAN 127.0.0.1/localhost + client CN=remote).
func writeTestTLSBundle(dir string) testTLSPaths {
	GinkgoHelper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())

	now := time.Now().Add(-time.Minute)
	caSerial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	Expect(err).NotTo(HaveOccurred())

	caTemplate := &x509.Certificate{
		SerialNumber:          caSerial,
		Subject:               pkix.Name{CommonName: "submariner-cilium-cm-ca"},
		NotBefore:             now,
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	Expect(err).NotTo(HaveOccurred())
	caCert, err := x509.ParseCertificate(caDER)
	Expect(err).NotTo(HaveOccurred())

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	serverSerial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	Expect(err).NotTo(HaveOccurred())

	serverTemplate := &x509.Certificate{
		SerialNumber: serverSerial,
		Subject:      pkix.Name{CommonName: "submariner-cilium-cm"},
		NotBefore:    now,
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	Expect(err).NotTo(HaveOccurred())

	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	clientSerial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	Expect(err).NotTo(HaveOccurred())

	clientTemplate := &x509.Certificate{
		SerialNumber: clientSerial,
		Subject:      pkix.Name{CommonName: "remote"},
		NotBefore:    now,
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	clientDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caCert, &clientKey.PublicKey, caKey)
	Expect(err).NotTo(HaveOccurred())

	paths := testTLSPaths{
		caCert:     filepath.Join(dir, "ca.crt"),
		serverCert: filepath.Join(dir, "tls.crt"),
		serverKey:  filepath.Join(dir, "tls.key"),
		clientCert: filepath.Join(dir, "client.crt"),
		clientKey:  filepath.Join(dir, "client.key"),
	}

	writePEMCert(paths.caCert, caDER)
	writePEMCert(paths.serverCert, serverDER)
	writePEMCert(paths.clientCert, clientDER)
	writePEMKey(paths.serverKey, serverKey)
	writePEMKey(paths.clientKey, clientKey)

	return paths
}

func writePEMCert(path string, der []byte) {
	GinkgoHelper()
	Expect(os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)).To(Succeed())
}

func writePEMKey(path string, key *ecdsa.PrivateKey) {
	GinkgoHelper()

	der, err := x509.MarshalECPrivateKey(key)
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600)).To(Succeed())
}
