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

// CertificateHandler handles NSS database operations for certificates.
type CertificateHandler struct {
	clusterID    string
	nssDBDir     string
	lastCertHash string
}

func NewCertificateHandler(clusterID string) *CertificateHandler {
	return &CertificateHandler{
		clusterID: clusterID,
		nssDBDir:  RootDir + "/var/lib/ipsec/nss",
	}
}

func (c *CertificateHandler) initNSSDatabase(ctx context.Context) error {
	if _, err := os.Stat(c.NSSDatabaseFile()); err == nil {
		certLogger.Info("NSS database already exists, using existing database")
		return nil
	}

	certLogger.Info("NSS database does not exist, initializing new database")

	//nolint:gosec // certutil args are from trusted config
	cmd := command.New(exec.CommandContext(ctx, "certutil", "-N", "-d", "sql:"+c.nssDBDir, "--empty-password"))

	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, "failed to initialize NSS database")
	}

	certLogger.Info("NSS database initialized successfully")

	return nil
}

func (c *CertificateHandler) loadCertificatesIntoNSS(ctx context.Context, tlsCert, tlsKey, caCert []byte) error {
	// Load CA certificate
	if err := c.loadCertificate(ctx, caCert, "ca-cert", "C,,", "c"); err != nil {
		return errors.Wrap(err, "failed to load CA certificate")
	}

	// Load client certificate
	if err := c.loadCertificate(ctx, tlsCert, "client-cert", "C,,", "c"); err != nil {
		return errors.Wrap(err, "failed to load client certificate")
	}

	// Load client private key
	err := c.loadPrivateKey(ctx, tlsKey, "client-key")

	return errors.Wrap(err, "failed to load client private key")
}

func (c *CertificateHandler) loadCertificate(ctx context.Context, certData []byte, nickname, trustFlags, certType string) error {
	//nolint:gosec // certutil args are from trusted config
	execCmd := exec.CommandContext(ctx, "certutil", "-A", "-d", "sql:"+c.nssDBDir, "-n", nickname, "-t", trustFlags, "-i", "-", "-a")
	execCmd.Stdin = bytes.NewReader(certData)

	cmd := command.New(execCmd)

	if err := cmd.Run(); err != nil {
		return errors.Wrapf(err, "failed to load %s", certType)
	}

	return nil
}

func (c *CertificateHandler) loadPrivateKey(ctx context.Context, keyData []byte, nickname string) error {
	//nolint:gosec // certutil args are from trusted config
	execCmd := exec.CommandContext(ctx, "certutil", "-A", "-d", "sql:"+c.nssDBDir, "-n", nickname, "-t", "u,u,u", "-i", "-", "-a")
	execCmd.Stdin = bytes.NewReader(keyData)

	cmd := command.New(execCmd)

	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, "failed to load private key")
	}

	return nil
}

func (c *CertificateHandler) Cleanup() {
	certLogger.Info("Cleaning up certificate handler")
	c.cleanupCertificateFromNSS()
}

func (c *CertificateHandler) cleanupCertificateFromNSS() {
	certName := "submariner-client-" + c.clusterID
	caName := "submariner-ca"
	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)

	defer cancel()

	// Delete client certificate
	//nolint:gosec // certutil args are from trusted config
	cmd := command.New(exec.CommandContext(ctx, "certutil", "-D", "-d", "sql:"+c.nssDBDir, "-n", certName))

	if err := cmd.Run(); err != nil {
		certLogger.Warningf("Failed to delete client certificate from NSS database: %v", err)
	} else {
		certLogger.Infof("Deleted Submariner client certificate from NSS database: %s", certName)
	}

	// Delete CA certificate
	//nolint:gosec // certutil args are from trusted config
	cmd = command.New(exec.CommandContext(ctx, "certutil", "-D", "-d", "sql:"+c.nssDBDir, "-n", caName))

	if err := cmd.Run(); err != nil {
		certLogger.Warningf("Failed to delete CA certificate from NSS database: %v", err)
	} else {
		certLogger.Infof("Deleted Submariner CA certificate from NSS database: %s", caName)
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
