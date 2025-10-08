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
	"maps"
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/certificate"
	fakecommand "github.com/submariner-io/admiral/pkg/command/fake"
	"github.com/submariner-io/submariner/pkg/cable/libreswan"
)

var _ = Describe("CertificateHandler", func() {
	certData := map[string][]byte{
		certificate.CADataKey:         []byte("-----BEGIN CERTIFICATE-----\nMOCK_CA_CERT\n-----END CERTIFICATE-----"),
		certificate.TLSDataKey:        []byte("-----BEGIN CERTIFICATE-----\nMOCK_CLIENT_CERT\n-----END CERTIFICATE-----"),
		certificate.PrivateKeyDataKey: []byte("-----BEGIN PRIVATE KEY-----\nMOCK_CLIENT_KEY\n-----END PRIVATE KEY-----"),
	}

	var (
		cmdExecutor *fakecommand.Executor
		handler     *libreswan.CertificateHandler
	)

	BeforeEach(func() {
		setupTempDir()

		cmdExecutor = fakecommand.New()
		handler = libreswan.NewCertificateHandler("test-cluster")
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
			Expect(handler.OnSignedCallback(certData)).To(Succeed())

			cmdExecutor.AwaitCommand(ContainSubstring("certutil"), "-N")
			assertCmdStdIn(cmdExecutor.AwaitCommand(ContainSubstring("certutil"), "-A", libreswan.CACertName),
				certData[certificate.CADataKey])
			assertCmdStdIn(cmdExecutor.AwaitCommand(ContainSubstring("certutil"), "-A", libreswan.ClientCertName),
				certData[certificate.TLSDataKey])
			assertCmdStdIn(cmdExecutor.AwaitCommand(ContainSubstring("certutil"), "-A", libreswan.ClientKeyName),
				certData[certificate.PrivateKeyDataKey])
			cmdExecutor.Clear()

			By("Invoking OnSignedCallback with new cert data")

			newCertData := map[string][]byte{
				certificate.CADataKey:         []byte("NEW_CA_CERT"),
				certificate.TLSDataKey:        []byte("NEW_CLIENT_CERT"),
				certificate.PrivateKeyDataKey: []byte("NEW_CLIENT_KEY"),
			}
			Expect(handler.OnSignedCallback(newCertData)).To(Succeed())

			cmdExecutor.AwaitCommand(ContainSubstring("certutil"), "-A", libreswan.CACertName)
			cmdExecutor.AwaitCommand(ContainSubstring("certutil"), "-A", libreswan.ClientCertName)
			cmdExecutor.AwaitCommand(ContainSubstring("certutil"), "-A", libreswan.ClientKeyName)
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

			Expect(handler.OnSignedCallback(certData)).NotTo(Succeed())
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

			Expect(handler.OnSignedCallback(certData)).NotTo(Succeed())
		})

		It("should only initialize the NSS database once", func() {
			Expect(handler.OnSignedCallback(certData)).To(Succeed())

			cmdExecutor.AwaitCommand(ContainSubstring("certutil"), "-N")
			cmdExecutor.Clear()

			nssDBFile := handler.NSSDatabaseFile()
			Expect(os.MkdirAll(filepath.Dir(nssDBFile), 0o700)).To(Succeed())
			_, err := os.Create(nssDBFile)
			Expect(err).NotTo(HaveOccurred())

			newCertData := maps.Clone(certData)
			newCertData[certificate.CADataKey] = []byte("NEW_CA_CERT")
			Expect(handler.OnSignedCallback(newCertData)).To(Succeed())

			cmdExecutor.EnsureNoCommand(ContainSubstring("certutil"), "-N")
		})
	})

	Context("Cleanup", func() {
		It("should delete certificates from the NSS database", func() {
			handler.Cleanup(context.TODO())

			cmdExecutor.AwaitCommand(ContainSubstring("certutil"), "-D", libreswan.CACertName)
			cmdExecutor.AwaitCommand(ContainSubstring("certutil"), "-D", libreswan.ClientCertName)
			cmdExecutor.AwaitCommand(ContainSubstring("certutil"), "-D", libreswan.ClientKeyName)
		})
	})
})

type mockSigningRequestor struct {
	issuedCh chan []string
}

func (m *mockSigningRequestor) Issue(_ context.Context, _ string, sanIPs []string, onSigned certificate.OnSignedFn) error {
	if m.issuedCh != nil {
		m.issuedCh <- sanIPs
	}

	certData := map[string][]byte{
		certificate.TLSDataKey:        []byte("mock-tls-cert"),
		certificate.PrivateKeyDataKey: []byte("mock-tls-key"),
		certificate.CADataKey:         []byte("mock-ca-cert"),
	}

	return onSigned(certData)
}

func (m *mockSigningRequestor) Uninstall(_ context.Context) error {
	return nil
}

func (m *mockSigningRequestor) Remove(_ context.Context, name string) error {
	return nil
}
