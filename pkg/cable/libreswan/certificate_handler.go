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
	ClientKeyName  = "subm-client-key"
)

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

	if err := execCertUtil(command.New(c.newCertUtilCmd(ctx, "-N", "--empty-password"))); err != nil {
		return errors.Wrap(err, "failed to initialize NSS database")
	}

	certLogger.Info("NSS database initialized successfully")

	return nil
}

func (c *CertificateHandler) loadCertificatesIntoNSS(ctx context.Context, tlsCert, tlsKey, caCert []byte) error {
	// Load CA certificate
	if err := c.loadCertificate(ctx, caCert, CACertName, "C,,"); err != nil {
		return errors.Wrap(err, "failed to load CA certificate")
	}

	// Load client certificate
	if err := c.loadCertificate(ctx, tlsCert, ClientCertName, "C,,"); err != nil {
		return errors.Wrap(err, "failed to load client certificate")
	}

	// Load client private key
	err := c.loadPrivateKey(ctx, tlsKey, ClientKeyName)

	return errors.Wrap(err, "failed to load client private key")
}

func (c *CertificateHandler) loadCertificate(ctx context.Context, certData []byte, nickname, trustFlags string) error {
	execCmd := c.newCertUtilCmd(ctx, "-A", "-n", nickname, "-t", trustFlags, "-i", "-", "-a")
	execCmd.Stdin = bytes.NewReader(certData)

	cmd := command.New(execCmd)

	if err := execCertUtil(cmd); err != nil {
		return errors.Wrapf(err, "failed to load certificate %q", nickname)
	}

	return nil
}

func (c *CertificateHandler) loadPrivateKey(ctx context.Context, keyData []byte, nickname string) error {
	execCmd := c.newCertUtilCmd(ctx, "-A", "-n", nickname, "-t", "u,u,u", "-i", "-", "-a")
	execCmd.Stdin = bytes.NewReader(keyData)

	cmd := command.New(execCmd)

	if err := execCertUtil(cmd); err != nil {
		return errors.Wrap(err, "failed to load private key")
	}

	return nil
}

func (c *CertificateHandler) Cleanup(ctx context.Context) {
	certLogger.Info("Cleaning up certificate handler")

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	for _, certName := range []string{CACertName, ClientCertName, ClientKeyName} {
		err := execCertUtil(command.New(c.newCertUtilCmd(ctx, "-D", "-n", certName)))
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

func (c *CertificateHandler) newCertUtilCmd(ctx context.Context, args ...string) *exec.Cmd {
	//nolint:gosec // certutil args are from trusted config
	return exec.CommandContext(ctx, "certutil", append(args, "-d", "sql:"+c.nssDBDir)...)
}

func execCertUtil(cmd command.Interface) error {
	out, err := cmd.CombinedOutput()
	return errors.Wrapf(err, "failed to execute certutil: %s", string(out))
}
