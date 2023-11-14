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
	apiError "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/utils/net"
)

type ConnectionHandler struct {
	k8sClientset  clientset.Interface
	dynamicClient dynamic.Interface
	nbdb          libovsdbclient.Client
}

func NewConnectionHandler(k8sClientset clientset.Interface, dynamicClient dynamic.Interface) *ConnectionHandler {
	return &ConnectionHandler{
		k8sClientset:  k8sClientset,
		dynamicClient: dynamicClient,
	}
}

func (c *ConnectionHandler) initClients(newOVSDBClient NewOVSDBClientFn) error {
	// Create nbdb client
	nbdbModel, err := nbdb.FullDatabaseModel()
	if err != nil {
		return errors.Wrap(err, "error getting OVN NBDB database model")
	}

	c.nbdb, err = c.createLibovsdbClient(nbdbModel, newOVSDBClient)
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

func (c *ConnectionHandler) createLibovsdbClient(dbModel model.ClientDBModel, newClient NewOVSDBClientFn) (libovsdbclient.Client, error) {
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

	dbAddress, err := discoverOvnKubernetesNetwork(context.TODO(), c.k8sClientset, c.dynamicClient, zoneName)
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

	client, err := newClient(dbModel, options...)
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

func discoverOvnKubernetesNetwork(ctx context.Context, k8sClientset clientset.Interface,
	dynamicClient dynamic.Interface, zoneName string,
) (string, error) {
	openshiftNetwork, err := FindOpenshiftNetwork(ctx, dynamicClient)
	if err != nil {
		return "", err
	}

	if openshiftNetwork != nil {
		return discoverOpenshiftOvnKubernetesNetwork(ctx, k8sClientset)
	}

	return discoverKindOvnKubernetesNetwork(ctx, k8sClientset, zoneName)
}

/*
	OpenShift non-ic: Identify pods labeled as "app=ovnkube-node" along with a corresponding service named ovnkube-db.
	When we locate such pods and services, we use the defaultOpenshiftOVNNBDB path.
	OpenShift with OVN-IC and one node per zone: If the ovnkube-db service does not exist, we use the
	defaultOVNOpenshiftUnixSocket path.
*/

func discoverOpenshiftOvnKubernetesNetwork(ctx context.Context, k8sClientSet clientset.Interface) (string, error) {
	ovnPod, err := FindPod(ctx, k8sClientSet, ovnPodLabel)
	if err != nil {
		return "", errors.Wrapf(err, "error finding a pod with label %q", ovnPodLabel)
	}

	_, err = k8sClientSet.CoreV1().Services(ovnPod.Namespace).Get(ctx, ovnKubeService, metav1.GetOptions{})
	if err == nil {
		return defaultOpenshiftOVNNBDB, nil
	}

	return defaultOVNOpenshiftUnixSocket, nil
}

/*
The discovery method varies based on the deployment type.

	kind non-ic: Search for the db pod with label "name=ovnkube-db" and if its present, use the protocol and other
	details from the pod.

	kind ic with multiple nodes per zone: Parse every endpoint in the namespace where OVN pods are running and find the
	one that has same zone as the node in which route-agent runs. To get the ovn namespace we use the namespace of the
	pods with label app=ovnkube-node.

	kind ic with one node per zone: we will have no endpoints matching hence we will return default kind socket path
*/

func discoverKindOvnKubernetesNetwork(ctx context.Context, k8sClientSet clientset.Interface, zoneName string) (string, error) {
	ovnDBPod, err := FindPod(ctx, k8sClientSet, "name=ovnkube-db")
	if err != nil {
		return "", err
	}

	if ovnDBPod != nil {
		return discoverKindOvnDBClusterNetwork(ctx, ovnDBPod, k8sClientSet)
	}

	ovnPod, err := FindPod(ctx, k8sClientSet, ovnPodLabel)
	if err != nil {
		return "", err
	}

	if ovnPod == nil {
		return "", fmt.Errorf("error finding the pod with label %q", ovnPodLabel)
	}

	return discoverKindOvnNodeClusterNetwork(ctx, k8sClientSet, zoneName, ovnPod)
}

func discoverKindOvnDBClusterNetwork(ctx context.Context, ovnDBPod *corev1.Pod, k8sClientSet clientset.Interface) (string, error) {
	_, err := k8sClientSet.CoreV1().Services(ovnDBPod.Namespace).Get(ctx, ovnKubeService, metav1.GetOptions{})
	if err != nil {
		return "", errors.Wrap(err, "OVN DB service is not present but expected")
	}

	dbConnectionProtocol := findProtocol(ovnDBPod)

	return fmt.Sprintf("%s:%s.%s:%d", dbConnectionProtocol, ovnKubeService, ovnDBPod.Namespace, ovnNBDBDefaultPort), nil
}

func discoverKindOvnNodeClusterNetwork(ctx context.Context, k8sClientset clientset.Interface,
	zoneName string, ovnPod *corev1.Pod,
) (string, error) {
	endpointList, err := findEndpoint(ctx, k8sClientset, ovnPod.Namespace)
	if err != nil {
		return "", errors.Wrapf(err, "error retrieving the endpoints from namespace %q", ovnPod.Namespace)
	}

	var nbdbAddress string

	if endpointList == nil || len(endpointList.Items) == 0 {
		nbdbAddress = defaultOVNUnixSocket
	} else {
		nbdbAddress, err = createClusterNetworkWithEndpoints(endpointList.Items, zoneName)
		if err != nil {
			return "", err
		}
	}

	return nbdbAddress, nil
}

func createClusterNetworkWithEndpoints(endPoints []corev1.Endpoints, zoneName string) (string, error) {
	for index := range endPoints {
		for _, subset := range endPoints[index].Subsets {
			if strings.Contains(endPoints[index].Name, zoneName) {
				for _, port := range subset.Ports {
					if strings.Contains(port.Name, "north") && net.IsIPv4String(subset.Addresses[0].IP) {
						return fmt.Sprintf("%s:%s:%d",
							port.Protocol, subset.Addresses[0].IP, ovnNBDBDefaultPort), nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("error finding an endpoint for the zone %q", zoneName)
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

//nolint:nilnil // Intentional as the purpose is to find.
func FindOpenshiftNetwork(ctx context.Context, dynamicClient dynamic.Interface) (*unstructured.Unstructured, error) {
	networkClient := dynamicClient.Resource(
		schema.GroupVersionResource{
			Group:    "config.openshift.io",
			Version:  "v1",
			Resource: "networks",
		},
	)

	networks, err := networkClient.Get(ctx, "cluster", metav1.GetOptions{})

	if apiError.IsNotFound(err) {
		return nil, nil
	}

	return networks, errors.Wrapf(err, "error finding the openshift network CR")
}
