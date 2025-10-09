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

package libreswan

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/certificate"
	"github.com/submariner-io/admiral/pkg/command"
	"github.com/submariner-io/admiral/pkg/log"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var certLogger = log.Logger{Logger: logf.Log.WithName("CertHandler")}

const (
	CACertName     = "subm-ca-cert"
	ClientCertName = "subm-client-cert"
)

// CertificateHandler handles NSS database operations for certificates.
type CertificateHandler struct {
	nssDBDir     string
	lastCertHash string
}

func NewCertificateHandler() *CertificateHandler {
	return &CertificateHandler{
		nssDBDir: RootDir + "/var/lib/ipsec/nss",
	}
}

func (c *CertificateHandler) initNSSDatabase(ctx context.Context) error {
	if _, err := os.Stat(c.NSSDatabaseFile()); err == nil {
		certLogger.Info("NSS database already exists, using existing database")
		return nil
	}

	certLogger.Info("NSS database does not exist, initializing new database")

	if err := execWithOutput(command.New(c.newCertUtilCmd(ctx, "-N", "--empty-password"))); err != nil {
		return errors.Wrap(err, "failed to initialize NSS database")
	}

	certLogger.Info("NSS database initialized successfully")

	return nil
}

func (c *CertificateHandler) loadCertificatesIntoNSS(ctx context.Context, tlsCert, tlsKey, caCert []byte) error {
	// Load CA certificate
	if err := c.loadCertificate(ctx, caCert, CACertName, "CT,"); err != nil {
		return errors.Wrap(err, "failed to load CA certificate")
	}

	// Load client certificate and key using pk12util
	err := c.loadPrivateKey(ctx, tlsCert, tlsKey, ClientCertName)

	return errors.Wrap(err, "failed to load client certificate with key")
}

func (c *CertificateHandler) loadCertificate(ctx context.Context, certData []byte, nickname, trustFlags string) error {
	execCmd := c.newCertUtilCmd(ctx, "-A", "-n", nickname, "-t", trustFlags, "-a")
	execCmd.Stdin = bytes.NewReader(certData)

	err := execWithOutput(command.New(execCmd))

	return errors.Wrapf(err, "failed to load certificate %q", nickname)
}

//nolint:gosec // openssl/pk12util args are from trusted config
func (c *CertificateHandler) loadPrivateKey(ctx context.Context, certData, keyData []byte, nickname string) error {
	// Write cert and key to temporary files
	certFile, err := os.CreateTemp(RootDir, "submariner-cert-*.crt")
	if err != nil {
		return errors.Wrap(err, "failed to create temporary cert file")
	}
	defer os.Remove(certFile.Name())

	if _, err := certFile.Write(certData); err != nil {
		return errors.Wrap(err, "failed to write certificate to temporary file")
	}

	certFile.Close()

	keyFile, err := os.CreateTemp(RootDir, "submariner-key-*.key")
	if err != nil {
		return errors.Wrap(err, "failed to create temporary key file")
	}
	defer os.Remove(keyFile.Name())

	if _, err := keyFile.Write(keyData); err != nil {
		return errors.Wrap(err, "failed to write key to temporary file")
	}

	keyFile.Close()

	// Create PKCS#12 file with openssl
	p12File, err := os.CreateTemp(RootDir, "submariner-client-*.p12")
	if err != nil {
		return errors.Wrap(err, "failed to create temporary pkcs12 file")
	}

	defer os.Remove(p12File.Name())
	p12File.Close()

	// Use empty password for PKCS#12
	pkcs12Password := ""

	opensslCmd := exec.CommandContext(ctx, "openssl", "pkcs12", "-export",
		"-in", certFile.Name(),
		"-inkey", keyFile.Name(),
		"-out", p12File.Name(),
		"-name", nickname,
		"-passout", "pass:"+pkcs12Password)
	if err := execWithOutput(command.New(opensslCmd)); err != nil {
		return errors.Wrap(err, "failed to create PKCS#12 file")
	}

	// Import PKCS#12 into NSS using pk12util
	pk12Cmd := exec.CommandContext(ctx, "pk12util", "-i", p12File.Name(), "-d", "sql:"+c.nssDBDir, "-W", pkcs12Password)
	err = execWithOutput(command.New(pk12Cmd))

	return errors.Wrap(err, "failed to import PKCS#12 into NSS database")
}

func (c *CertificateHandler) Cleanup(ctx context.Context) {
	certLogger.Info("Cleaning up certificate handler")

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	for _, certName := range []string{CACertName, ClientCertName} {
		err := execWithOutput(command.New(c.newCertUtilCmd(ctx, "-D", "-n", certName)))
		if err != nil {
			certLogger.Errorf(err, "Failed to delete certificate %q from NSS database", certName)
		} else {
			certLogger.Infof("Deleted certificate %q from NSS database", certName)
		}
	}
}

// OnSignedCallback implements the OnSignedFn callback for admiral's SigningRequestor.
func (c *CertificateHandler) OnSignedCallback(secretData map[string][]byte) error {
	// Extract certificate data
	tlsCert := secretData[certificate.TLSDataKey]
	tlsKey := secretData[certificate.PrivateKeyDataKey]
	caCert := secretData[certificate.CADataKey]

	// Compute hash of cert+key+ca to avoid reloading the same certificates
	certData := string(tlsCert) + string(tlsKey) + string(caCert)
	certHash := fmt.Sprintf("%x", sha256.Sum256([]byte(certData)))

	if certHash == c.lastCertHash {
		certLogger.V(log.TRACE).Info("Certificate data unchanged, skipping NSS loading")
		return nil
	}

	certLogger.Info("Loading certificates into the NSS database via callback")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := c.initNSSDatabase(ctx); err != nil {
		return errors.Wrap(err, "failed to initialize NSS database")
	}

	if err := c.loadCertificatesIntoNSS(ctx, tlsCert, tlsKey, caCert); err != nil {
		return errors.Wrap(err, "failed to load certificates into NSS database")
	}

	certLogger.Info("Certificates successfully loaded into NSS database")

	c.lastCertHash = certHash

	return nil
}

func (c *CertificateHandler) NSSDatabaseFile() string {
	return c.nssDBDir + "/cert9.db"
}

func (c *CertificateHandler) NSSDatabaseDir() string {
	return c.nssDBDir
}

func (c *CertificateHandler) newCertUtilCmd(ctx context.Context, args ...string) *exec.Cmd {
	//nolint:gosec // certutil args are from trusted config
	return exec.CommandContext(ctx, "certutil", append(args, "-d", "sql:"+c.nssDBDir)...)
}

func execWithOutput(cmd command.Interface) error {
	out, err := cmd.CombinedOutput()
	return errors.Wrapf(err, "failed to execute certutil: %s", string(out))
}
