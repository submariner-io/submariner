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

package libreswan_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/certificate"
	fakecommand "github.com/submariner-io/admiral/pkg/command/fake"
	"github.com/submariner-io/submariner/pkg/cable/libreswan"
)

var _ = Describe("CertificateHandler", func() {
	var (
		cmdExecutor  *fakecommand.Executor
		handler      *libreswan.CertificateHandler
		testCertData map[string][]byte
		newCertData  map[string][]byte
	)

	BeforeEach(func() {
		// CA
		caKey, caCert, err := certificate.CreateCAKeyAndCertificate("CA", 24*365*10*time.Hour)
		Expect(err).NotTo(HaveOccurred())
		caDER, err := x509.CreateCertificate(rand.Reader, caCert, caCert, &caKey.PublicKey, caKey)
		Expect(err).NotTo(HaveOccurred())

		caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

		createSignedCertificate := func(name string) map[string][]byte {
			privateKey, err := rsa.GenerateKey(rand.Reader, certificate.RSABitSize)
			Expect(err).NotTo(HaveOccurred())

			serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
			Expect(err).NotTo(HaveOccurred())

			cert := &x509.Certificate{
				SerialNumber: serialNumber,
				Subject: pkix.Name{
					CommonName:   name,
					Organization: []string{"submariner.io"},
				},
				NotBefore:   time.Now(),
				NotAfter:    time.Now().AddDate(10, 0, 0),
				KeyUsage:    x509.KeyUsageDigitalSignature,
				ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			}

			certDER, err := x509.CreateCertificate(rand.Reader, cert, caCert, &privateKey.PublicKey, caKey)
			Expect(err).NotTo(HaveOccurred())

			return map[string][]byte{
				certificate.CADataKey:         caPEM,
				certificate.TLSDataKey:        pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}),
				certificate.PrivateKeyDataKey: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}),
			}
		}

		// First test certificate
		testCertData = createSignedCertificate("test")

		// New test certificate
		newCertData = createSignedCertificate("new")
	})

	BeforeEach(func() {
		setupTempDir()

		cmdExecutor = fakecommand.New()
		handler = libreswan.NewCertificateHandler()
		Expect(handler).NotTo(BeNil())
		DeferCleanup(cmdExecutor.Clear)
	})

	Context("OnSignedCallback", func() {
		assertCmdStdIn := func(cmd *exec.Cmd, expBytes []byte) {
			data := make([]byte, len(expBytes))
			n, err := cmd.Stdin.Read(data)
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(len(expBytes)))
			Expect(data).To(Equal(expBytes))
		}

		It("should successfully load the certificates into the NSS database", func() {
			Expect(handler.OnSignedCallback(testCertData)).To(Succeed())

			cmdExecutor.AwaitCommand(ContainSubstring("certutil"), "-N", "-d", "sql:"+handler.NSSDatabaseDir())
			assertCmdStdIn(cmdExecutor.AwaitCommand(ContainSubstring("certutil"), "-A", libreswan.CACertName,
				"-d", "sql:"+handler.NSSDatabaseDir()), testCertData[certificate.CADataKey])
			cmdExecutor.AwaitCommand(ContainSubstring("pk12util"), "-d", "sql:"+handler.NSSDatabaseDir())
			cmdExecutor.Clear()

			By("Invoking OnSignedCallback with new cert data")
			Expect(handler.OnSignedCallback(newCertData)).To(Succeed())

			cmdExecutor.AwaitCommand(ContainSubstring("certutil"), "-A", libreswan.CACertName)
			cmdExecutor.Clear()

			By("Invoking OnSignedCallback with unchanged cert data")

			Expect(handler.OnSignedCallback(newCertData)).To(Succeed())

			cmdExecutor.EnsureNoCommand(ContainSubstring("certutil"))
		})

		It("should handle NSS database initialization failure", func() {
			cmdExecutor = fakecommand.NewWithInterceptor(func(cmd *exec.Cmd) fakecommand.InterceptorFuncs {
				if fakecommand.CmdMatches(cmd, ContainSubstring("certutil"), "-N") {
					return fakecommand.InterceptorFuncs{CombinedOutput: func() ([]byte, error) {
						return []byte("database init failed"), errors.New("exit status 255")
					}}
				}

				return fakecommand.InterceptorFuncs{}
			})

			Expect(handler.OnSignedCallback(testCertData)).NotTo(Succeed())
		})

		It("should handle certificate loading failure", func() {
			cmdExecutor = fakecommand.NewWithInterceptor(func(cmd *exec.Cmd) fakecommand.InterceptorFuncs {
				if fakecommand.CmdMatches(cmd, ContainSubstring("certutil"), "-A", libreswan.CACertName) {
					return fakecommand.InterceptorFuncs{CombinedOutput: func() ([]byte, error) {
						return []byte("certificate load failed"), errors.New("exit status 255")
					}}
				}

				return fakecommand.InterceptorFuncs{}
			})

			Expect(handler.OnSignedCallback(testCertData)).NotTo(Succeed())
		})

		It("should only initialize the NSS database once", func() {
			Expect(handler.OnSignedCallback(testCertData)).To(Succeed())

			cmdExecutor.AwaitCommand(ContainSubstring("certutil"), "-N")
			cmdExecutor.Clear()

			nssDBFile := handler.NSSDatabaseFile()
			Expect(os.MkdirAll(filepath.Dir(nssDBFile), 0o700)).To(Succeed())
			f, err := os.Create(nssDBFile)
			Expect(err).NotTo(HaveOccurred())
			Expect(f.Close()).NotTo(HaveOccurred())

			Expect(handler.OnSignedCallback(newCertData)).To(Succeed())

			cmdExecutor.EnsureNoCommand(ContainSubstring("certutil"), "-N")
		})
	})

	Context("Cleanup", func() {
		It("should delete certificates from the NSS database", func() {
			handler.Cleanup(context.TODO())

			cmdExecutor.AwaitCommand(ContainSubstring("certutil"), "-D", libreswan.CACertName)
			cmdExecutor.AwaitCommand(ContainSubstring("certutil"), "-D", libreswan.ClientCertName)
		})
	})
})
