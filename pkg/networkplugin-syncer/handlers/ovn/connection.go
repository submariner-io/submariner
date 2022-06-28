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
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	libovsdbclient "github.com/ovn-org/libovsdb/client"
	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/nbdb"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/sbdb"
	"github.com/pkg/errors"
	"github.com/submariner-io/submariner/pkg/util/clusterfiles"
	"k8s.io/klog"
	"k8s.io/klog/v2/klogr"
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
	} else {
		klog.Infof("OVN connection using plaintext TCP")
	}

	// Create nbdb client
	nbdbModel, err := nbdb.FullDatabaseModel()
	if err != nil {
		return errors.Wrap(err, "error getting OVN NBDB database model")
	}

	ovn.nbdb, err = createLibovsdbClient(getOVNNBDBAddress(), tlsConfig, nbdbModel)
	if err != nil {
		return errors.Wrap(err, "error creating NBDB connection")
	}

	// create sbdb client
	sbdbModel, err := sbdb.FullDatabaseModel()
	if err != nil {
		return errors.Wrap(err, "error getting OVN NBDB database model")
	}

	ovn.sbdb, err = createLibovsdbClient(getOVNSBDBAddress(), tlsConfig, sbdbModel)
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

func createLibovsdbClient(dbAddress string, tlsConfig *tls.Config, dbModel model.ClientDBModel) (libovsdbclient.Client, error) {
	// following the same connection timeout ovn-k uses
	const connectTimeout time.Duration = 20 * time.Second
	logger := klogr.New()

	options := []libovsdbclient.Option{
		// Reading and parsing the DB after reconnect at scale can (unsurprisingly)
		// take longer than a normal ovsdb operation. Give it a bit more time so
		// we don't time out and enter a reconnect loop.
		libovsdbclient.WithReconnect(connectTimeout, &backoff.ZeroBackOff{}),
		libovsdbclient.WithLogger(&logger),
		libovsdbclient.WithEndpoint(dbAddress),
	}

	if tlsConfig != nil {
		options = append(options, libovsdbclient.WithTLSConfig(tlsConfig))
	}

	client, err := libovsdbclient.NewOVSDBClient(dbModel, options...)
	if err != nil {
		return nil, errors.Wrap(err, "error creating ovsdbClient")
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()

	err = client.Connect(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "error connecting to ovsdb")
	}

	if dbModel.Name() == "OVN_Northbound" {
		_, err = client.MonitorAll(ctx)
		if err != nil {
			client.Close()
			return nil, errors.Wrap(err, "error setting OVN NBDB client to monitor-all")
		}
	} else {
		// Only Monitor Required SBDB tables to reduce memory overhead
		_, err = client.Monitor(ctx,
			client.NewMonitor(
				libovsdbclient.WithTable(&sbdb.Chassis{}),
			),
		)
		if err != nil {
			client.Close()
			return nil, errors.Wrap(err, "error monitoring chassis table in OVN SBDB")
		}
	}

	return client, nil
}
