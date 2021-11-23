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

package ovn

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"strings"

	goovn "github.com/ebay/go-ovn"
	"github.com/pkg/errors"
	"github.com/submariner-io/submariner/pkg/networkplugin-syncer/handlers/ovn/nbctl"
	"github.com/submariner-io/submariner/pkg/util/clusterfiles"
	"k8s.io/klog"
)

func (ovn *SyncHandler) initClients() error {
	var tlsConfig *tls.Config

	if strings.HasPrefix(getOVNNBDBAddress(), "ssl:") || strings.HasPrefix(getOVNSBDBAddress(), "ssl:") {
		klog.Infof("OVN connection using SSL, loading certificates")

		certFile, err := clusterfiles.Get(ovn.k8sClientset, getOVNCertPath())
		if err != nil {
			return errors.Wrapf(err, "error getting config for %q", getOVNCertPath())
		}

		pkFile, err := clusterfiles.Get(ovn.k8sClientset, getOVNPrivKeyPath())
		if err != nil {
			return errors.Wrapf(err, "error getting config for %q", getOVNPrivKeyPath())
		}

		caFile, err := clusterfiles.Get(ovn.k8sClientset, getOVNCaBundlePath())
		if err != nil {
			return errors.Wrapf(err, "error getting config for %q", getOVNCaBundlePath())
		}

		tlsConfig, err = getOVNTLSConfig(pkFile, certFile, caFile)
		if err != nil {
			return errors.Wrap(err, "error getting OVN TLS config")
		}

		ovn.nbctl = nbctl.New(getOVNNBDBAddress(), pkFile, certFile, caFile)
	} else {
		klog.Infof("OVN connection using plaintext TCP")

		ovn.nbctl = nbctl.New(getOVNNBDBAddress(), "", "", "")
	}

	var err error

	ovn.nbdb, err = goovn.NewClient(&goovn.Config{
		Addr:      getOVNNBDBAddress(),
		Reconnect: true,
		TLSConfig: tlsConfig,
		Db:        goovn.DBNB,
	})

	if err != nil {
		return errors.Wrap(err, "error creating NBDB connection")
	}

	ovn.sbdb, err = goovn.NewClient(&goovn.Config{
		Addr:      getOVNSBDBAddress(),
		Reconnect: true,
		TLSConfig: tlsConfig,
		Db:        goovn.DBSB,
	})

	if err != nil {
		return errors.Wrap(err, "error creating SBDB connection")
	}

	return nil
}

func getOVNTLSConfig(pkFile, certFile, caFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, pkFile)
	if err != nil {
		return nil, errors.Wrap(err, "Failure loading ovn certificates")
	}

	rootCAs := x509.NewCertPool()

	data, err := os.ReadFile(caFile)
	if err != nil {
		return nil, errors.Wrap(err, "failure loading OVNDB ca bundle")
	}

	rootCAs.AppendCertsFromPEM(data)

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      rootCAs,
		ServerName:   "ovn",
		MinVersion:   tls.VersionTLS12,
	}, nil
}
