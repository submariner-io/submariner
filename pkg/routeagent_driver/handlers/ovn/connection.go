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
	"fmt"
	"os"
	"strings"

	"github.com/cenkalti/backoff/v4"
	libovsdbclient "github.com/ovn-org/libovsdb/client"
	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/nbdb"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/sbdb"
	"github.com/pkg/errors"
	nodeutil "github.com/submariner-io/submariner/pkg/node"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/util/clusterfiles"
	clientset "k8s.io/client-go/kubernetes"
)

type ConnectionHandler struct {
	k8sClientset clientset.Interface
	nbdb         libovsdbclient.Client
}

func NewConnectionHandler(k8sClientset clientset.Interface) *ConnectionHandler {
	return &ConnectionHandler{
		k8sClientset: k8sClientset,
	}
}

func (c *ConnectionHandler) initClients() error {
	var tlsConfig *tls.Config

	if strings.HasPrefix(getOVNNBDBAddress(), "ssl:") {
		certFile, err := clusterfiles.Get(c.k8sClientset, getOVNCertPath())
		if err != nil {
			return errors.Wrapf(err, "error getting config for %q", getOVNCertPath())
		}

		pkFile, err := clusterfiles.Get(c.k8sClientset, getOVNPrivKeyPath())
		if err != nil {
			return errors.Wrapf(err, "error getting config for %q", getOVNPrivKeyPath())
		}

		caFile, err := clusterfiles.Get(c.k8sClientset, getOVNCaBundlePath())
		if err != nil {
			return errors.Wrapf(err, "error getting config for %q", getOVNCaBundlePath())
		}

		tlsConfig, err = getOVNTLSConfig(pkFile, certFile, caFile)
		if err != nil {
			return errors.Wrap(err, "error getting OVN TLS config")
		}
	}

	// Create nbdb client
	nbdbModel, err := nbdb.FullDatabaseModel()
	if err != nil {
		return errors.Wrap(err, "error getting OVN NBDB database model")
	}

	c.nbdb, err = c.createLibovsdbClient(getOVNNBDBAddress(), tlsConfig, nbdbModel)
	if err != nil {
		return errors.Wrap(err, "error creating NBDB connection")
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

func (c *ConnectionHandler) createLibovsdbClient(dbAddress string, tlsConfig *tls.Config,
	dbModel model.ClientDBModel,
) (libovsdbclient.Client, error) {
	options := []libovsdbclient.Option{
		// Reading and parsing the DB after reconnect at scale can (unsurprisingly)
		// take longer than a normal ovsdb operation. Give it a bit more time so
		// we don't time out and enter a reconnect loop.
		libovsdbclient.WithReconnect(OVSDBTimeout, &backoff.ZeroBackOff{}),
		libovsdbclient.WithLogger(&logger.Logger),
	}

	if strings.HasPrefix(dbAddress, "IC:") {
		node, err := nodeutil.GetLocalNode(c.k8sClientset)
		if err != nil {
			return nil, errors.Wrap(err, "error getting the node")
		}

		annotations := node.GetAnnotations()

		zoneName, ok := annotations[constants.OvnZoneAnnotation]
		if !ok {
			return nil, errors.Wrapf(err, "node %q is missing the %q "+
				"annotation", node.Name, constants.OvnZoneAnnotation)
		}

		zoneDBAddress := getICDBAddress(dbAddress, zoneName)
		options = append(options, libovsdbclient.WithEndpoint(zoneDBAddress))
	} else {
		options = append(options, libovsdbclient.WithEndpoint(dbAddress))
	}

	if tlsConfig != nil {
		options = append(options, libovsdbclient.WithTLSConfig(tlsConfig))
	}

	client, err := libovsdbclient.NewOVSDBClient(dbModel, options...)
	if err != nil {
		return nil, errors.Wrap(err, "error creating ovsdbClient")
	}

	ctx, cancel := context.WithTimeout(context.Background(), OVSDBTimeout)
	defer cancel()

	err = client.Connect(ctx)

	err = errors.Wrap(err, "error connecting to ovsdb")
	if err == nil {
		if dbModel.Name() == "OVN_Northbound" {
			_, err = client.MonitorAll(ctx)
			err = errors.Wrap(err, "error setting OVN NBDB client to monitor-all")
		} else {
			// Only Monitor Required SBDB tables to reduce memory overhead
			_, err = client.Monitor(ctx,
				client.NewMonitor(
					libovsdbclient.WithTable(&sbdb.Chassis{}),
				),
			)
			err = errors.Wrap(err, "error monitoring chassis table in OVN SBDB")
		}
	}

	if err != nil {
		client.Close()
		return nil, err
	}

	return client, nil
}

// Parses input and returns the DB address.
//
//		input: will be of the format IC:Zone1Endpoint:TCP:192.168.0.1:9641.
//	    zoneName: will be Zone1 for the above input.
func getICDBAddress(inputs, zoneName string) string {
	entries := strings.Split(inputs, ",")
	for _, entry := range entries {
		if strings.Contains(entry, zoneName) {
			parts := strings.Split(entry, ":")
			if len(parts) >= 5 && strings.Contains(parts[1], zoneName) {
				return fmt.Sprintf("%s:%s:%s", parts[2], parts[3], parts[4])
			}
		}
	}

	return ""
}
