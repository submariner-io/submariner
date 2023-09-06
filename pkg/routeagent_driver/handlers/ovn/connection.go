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
	"github.com/submariner-io/submariner/pkg/node"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/util/clusterfiles"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/utils/net"
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
	// Create nbdb client
	nbdbModel, err := nbdb.FullDatabaseModel()
	if err != nil {
		return errors.Wrap(err, "error getting OVN NBDB database model")
	}

	c.nbdb, err = c.createLibovsdbClient(nbdbModel)
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

func (c *ConnectionHandler) createLibovsdbClient(dbModel model.ClientDBModel) (libovsdbclient.Client, error) {
	options := []libovsdbclient.Option{
		// Reading and parsing the DB after reconnect at scale can (unsurprisingly)
		// take longer than a normal ovsdb operation. Give it a bit more time so
		// we don't time out and enter a reconnect loop.
		libovsdbclient.WithReconnect(ovsDBTimeout, &backoff.ZeroBackOff{}),
		libovsdbclient.WithLogger(&logger.Logger),
	}

	localNode, err := node.GetLocalNode(c.k8sClientset)
	if err != nil {
		return nil, errors.Wrap(err, "error getting the node")
	}

	annotations := localNode.GetAnnotations()
	// Will use empty zone if not found
	zoneName := annotations[constants.OvnZoneAnnotation]

	dbAddress, err := discoverOvnKubernetesNetwork(context.TODO(), c.k8sClientset, zoneName)
	if err != nil {
		return nil, errors.Wrap(err, "error getting the OVN NBDB Address")
	}

	options = append(options, libovsdbclient.WithEndpoint(dbAddress))

	if strings.HasPrefix(dbAddress, "ssl:") {
		tlsConfig, err := getTLSConfig(c.k8sClientset)
		if err != nil {
			return nil, err
		}

		options = append(options, libovsdbclient.WithTLSConfig(tlsConfig))
	}

	client, err := libovsdbclient.NewOVSDBClient(dbModel, options...)
	if err != nil {
		return nil, errors.Wrap(err, "error creating ovsdbClient")
	}

	ctx, cancel := context.WithTimeout(context.Background(), ovsDBTimeout)
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

func getTLSConfig(k8sClientset clientset.Interface) (*tls.Config, error) {
	certFile, err := clusterfiles.Get(k8sClientset, getOVNCertPath())
	if err != nil {
		return nil, errors.Wrapf(err, "error getting config for %q", getOVNCertPath())
	}

	pkFile, err := clusterfiles.Get(k8sClientset, getOVNPrivKeyPath())
	if err != nil {
		return nil, errors.Wrapf(err, "error getting config for %q", getOVNPrivKeyPath())
	}

	caFile, err := clusterfiles.Get(k8sClientset, getOVNCaBundlePath())
	if err != nil {
		return nil, errors.Wrapf(err, "error getting config for %q", getOVNCaBundlePath())
	}

	tlsConfig, err := getOVNTLSConfig(pkFile, certFile, caFile)
	if err != nil {
		return nil, errors.Wrap(err, "error getting OVN TLS config")
	}

	return tlsConfig, nil
}

/*
   The discovery method is different for each kind of deployment.
        kind non-ic: Get the db pod with label name=ovnkube-db, if present will get the protocol and other details from it

        openshift non-ic: in this case there will be no pod with name=ovnkube-db but there will be a ovnkube-db service,
        in this case we will return the default Openshift db path

        kind ic with multiple node per zone: we parse every endpoint in the namespace ovn uses and find the one that has
        same zone as the node in which route-agent runs. To get the ovn namespace we use pods running on app=ovnkube-node.

        kind ic with one node per zone: we will have no endpoints matching hence we will return default kind socket path

        openshift ic with one node per zone: we will have endpoints in the namespace ovn but none will match the zone as
        these are not db endpoints. Here we will return default Openshift socket path
*/

func discoverOvnKubernetesNetwork(ctx context.Context, k8sClientset clientset.Interface, zoneName string) (string, error) {
	ovnDBPod, err := FindPod(ctx, k8sClientset, "name=ovnkube-db")
	if err != nil {
		return "", err
	}

	ovnPod, err := FindPod(ctx, k8sClientset, "app=ovnkube-node")
	if err != nil {
		return "", err
	}

	if ovnPod == nil {
		return defaultOVNOpenshiftUnixSocket, nil
	}

	var nbdbAddress string
	_, err = k8sClientset.CoreV1().Services(ovnPod.Namespace).Get(ctx, ovnKubeService, metav1.GetOptions{})

	// Openshift deployments uses DB that are parts of the node pod and there will not be a separate db pod for non-ic.
	if ovnDBPod == nil && err == nil {
		return defaultOpenshiftOVNNBDB, nil
	}
	// Kind deployments will have a db pod and db service for non-ic.
	if ovnDBPod != nil && err == nil {
		// This is a Kind non-IC deployment
		nbdbAddress = discoverOvnDBClusterNetwork(ovnDBPod)
	} else {
		if zoneName == "" {
			return defaultOVNOpenshiftUnixSocket, nil
		}

		nbdbAddress, err = discoverOvnNodeClusterNetwork(ctx, k8sClientset, zoneName, ovnPod)
		if err != nil {
			return "", err
		}
	}

	return nbdbAddress, nil
}

func discoverOvnDBClusterNetwork(ovnDBPod *corev1.Pod) string {
	dbConnectionProtocol := findProtocol(ovnDBPod)

	return fmt.Sprintf("%s:%s.%s:%d", dbConnectionProtocol, ovnKubeService, ovnDBPod.Namespace, ovnNBDBDefaultPort)
}

func discoverOvnNodeClusterNetwork(ctx context.Context, k8sClientset clientset.Interface,
	zoneName string, ovnPod *corev1.Pod,
) (string, error) {
	endpointList, err := findEndpoint(ctx, k8sClientset, ovnPod.Namespace)
	if err != nil {
		return "", errors.Wrapf(err, "error retrieving the endpoints from namespace %q", ovnPod.Namespace)
	}

	var nbdbAddress string

	if endpointList == nil || len(endpointList.Items) == 0 {
		// Kind setup will not have endpoints when using socket mode.
		nbdbAddress = defaultOVNUnixSocket
	} else {
		nbdbAddress = createClusterNetworkWithEndpoints(endpointList.Items, zoneName)
	}

	return nbdbAddress, nil
}

func createClusterNetworkWithEndpoints(endPoints []corev1.Endpoints, zoneName string) string {
	for index := range endPoints {
		for _, subset := range endPoints[index].Subsets {
			if strings.Contains(endPoints[index].Name, zoneName) {
				for _, port := range subset.Ports {
					if strings.Contains(port.Name, "north") && net.IsIPv4String(subset.Addresses[0].IP) {
						return fmt.Sprintf("%s:%s:%d",
							port.Protocol, subset.Addresses[0].IP, ovnNBDBDefaultPort)
					}
				}
			}
		}
	}

	// Openshift will have endpoints but not related to ovn DB when using IC.
	return defaultOVNOpenshiftUnixSocket
}

func findEndpoint(ctx context.Context, k8sClientset clientset.Interface, endpointNameSpace string) (*corev1.EndpointsList, error) {
	endpointsList, err := k8sClientset.CoreV1().Endpoints(endpointNameSpace).List(ctx, metav1.ListOptions{})

	return endpointsList, errors.WithMessagef(err, "error listing endpoints in namespace %q", endpointNameSpace)
}

func findProtocol(pod *corev1.Pod) string {
	dbConnectionProtocol := "tcp"

	for i := range pod.Spec.Containers {
		for _, envVar := range pod.Spec.Containers[i].Env {
			if envVar.Name == "OVN_SSL_ENABLE" {
				if !strings.EqualFold(envVar.Value, "NO") {
					dbConnectionProtocol = "ssl"
				}
			}
		}
	}

	return dbConnectionProtocol
}

//nolint:nilnil // Intentional as the purpose is to find.
func FindPod(ctx context.Context, k8sClientset clientset.Interface, labelSelector string) (*corev1.Pod, error) {
	podsList, err := k8sClientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return nil, errors.WithMessagef(err, "error listing Pods by label selector %q", labelSelector)
	}

	if len(podsList.Items) == 0 {
		return nil, nil
	}

	return &podsList.Items[0], nil
}
