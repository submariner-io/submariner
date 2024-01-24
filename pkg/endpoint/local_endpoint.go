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

package endpoint

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/util"
	submv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cidr"
	"github.com/submariner-io/submariner/pkg/node"
	"github.com/submariner-io/submariner/pkg/port"
	"github.com/submariner-io/submariner/pkg/types"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/set"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var logger = log.Logger{Logger: logf.Log.WithName("Endpoint")}

type Local struct {
	spec      atomic.Value
	endpoints dynamic.ResourceInterface
}

func NewLocal(spec *submv1.EndpointSpec, dynClient dynamic.Interface, namespace string) *Local {
	l := &Local{
		endpoints: dynClient.Resource(submv1.EndpointGVR).Namespace(namespace),
	}

	l.spec.Store(*spec)

	return l
}

func (l *Local) Spec() *submv1.EndpointSpec {
	spec := l.spec.Load().(submv1.EndpointSpec)
	return &spec
}

func (l *Local) Resource() *submv1.Endpoint {
	spec := l.spec.Load().(submv1.EndpointSpec)
	return endpointFrom(&spec)
}

func endpointFrom(spec *submv1.EndpointSpec) *submv1.Endpoint {
	endpointName, err := spec.GenerateName()
	utilruntime.Must(err)

	return &submv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: endpointName,
		},
		Spec: *spec,
	}
}

func (l *Local) Update(ctx context.Context, mutate func(existing *submv1.EndpointSpec)) error {
	toUpdate := resource.MustToUnstructured(l.Resource())

	var newSpec *submv1.EndpointSpec

	err := util.Update[*unstructured.Unstructured](ctx, resource.ForDynamic(l.endpoints), toUpdate,
		func(existing *unstructured.Unstructured) (*unstructured.Unstructured, error) {
			ep := resource.MustFromUnstructured(existing, &submv1.Endpoint{})
			mutate(&ep.Spec)
			newSpec = &ep.Spec

			return resource.MustToUnstructured(ep), nil
		})
	if err == nil && newSpec != nil {
		l.spec.Store(*newSpec)
	}

	return err
}

func GetLocalSpec(submSpec *types.SubmarinerSpecification, k8sClient kubernetes.Interface,
	airGappedDeployment bool,
) (*submv1.EndpointSpec, error) {
	// We'll panic if submSpec is nil, this is intentional
	privateIP := GetLocalIP()

	gwNode, err := node.GetLocalNode(k8sClient)
	if err != nil {
		return nil, errors.Wrap(err, "getting information on the local node")
	}

	hostname, err := os.Hostname()
	if err != nil {
		return nil, errors.Wrap(err, "error getting hostname")
	}

	var localSubnets []string

	globalnetEnabled := false

	if len(submSpec.GlobalCidr) > 0 {
		localSubnets = submSpec.GlobalCidr
		globalnetEnabled = true
	} else {
		localSubnets = append(localSubnets, cidr.ExtractIPv4Subnets(submSpec.ServiceCidr)...)
		localSubnets = append(localSubnets, cidr.ExtractIPv4Subnets(submSpec.ClusterCidr)...)
	}

	backendConfig, err := getBackendConfig(gwNode)
	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(submSpec.PublicIP, submv1.LoadBalancer+":") {
		backendConfig[submv1.UsingLoadBalancer] = "true"
	}

	endpointSpec := &submv1.EndpointSpec{
		CableName:     fmt.Sprintf("submariner-cable-%s-%s", submSpec.ClusterID, strings.ReplaceAll(privateIP, ".", "-")),
		ClusterID:     submSpec.ClusterID,
		Hostname:      hostname,
		PrivateIP:     privateIP,
		NATEnabled:    submSpec.NATEnabled,
		Subnets:       localSubnets,
		Backend:       submSpec.CableDriver,
		BackendConfig: backendConfig,
	}

	publicIP, err := getPublicIP(submSpec, k8sClient, backendConfig, airGappedDeployment)
	if err != nil {
		return nil, errors.Wrap(err, "could not determine public IP")
	}

	endpointSpec.PublicIP = publicIP

	if submSpec.HealthCheckEnabled && !globalnetEnabled {
		// When globalnet is enabled, HealthCheckIP will be the globalIP assigned to the Active GatewayNode.
		// In a fresh deployment, globalIP annotation for the node might take few seconds. So we listen on NodeEvents
		// and update the endpoint HealthCheckIP (to globalIP) in datastoreSyncer at a later stage. This will trigger
		// the HealthCheck between the clusters.
		endpointSpec.HealthCheckIP, err = getCNIInterfaceIPAddress(submSpec.ClusterCidr)
		if err != nil {
			return nil, fmt.Errorf("error getting CNI Interface IP address: %w."+
				"Please disable the health check if your CNI does not expose a pod IP on the nodes", err)
		}
	}

	return endpointSpec, nil
}

func getBackendConfig(nodeObj *v1.Node) (map[string]string, error) {
	backendConfig, err := getNodeBackendConfig(nodeObj)
	if err != nil {
		return backendConfig, err
	}

	// If the node has no specific UDP port assigned for dataplane, expose the cluster default one.
	if _, ok := backendConfig[submv1.UDPPortConfig]; !ok {
		udpPort := os.Getenv("CE_IPSEC_NATTPORT")
		if udpPort == "" {
			udpPort = strconv.Itoa(port.ExternalTunnel)
		}

		backendConfig[submv1.UDPPortConfig] = udpPort
	}

	// Enable and publish the natt-discovery-port by default.
	if _, ok := backendConfig[submv1.NATTDiscoveryPortConfig]; !ok {
		backendConfig[submv1.NATTDiscoveryPortConfig] = strconv.Itoa(port.NATTDiscovery)
	}

	// TODO: we should allow the cable drivers to capture and expose BackendConfig settings, instead of doing
	//      it here.
	preferredServerStr := backendConfig[submv1.PreferredServerConfig]

	if preferredServerStr == "" {
		preferredServerStr = os.Getenv("CE_IPSEC_PREFERREDSERVER")
	}

	if preferredServerStr == "" {
		preferredServerStr = "false"
	}

	preferredServer, err := strconv.ParseBool(preferredServerStr)
	if err != nil {
		return backendConfig, errors.Wrapf(err, "error parsing CE_IPSEC_PREFERREDSERVER or node gateway.submariner."+
			"io/preferred-server annotation bool: %s", preferredServerStr)
	}

	backendConfig[submv1.PreferredServerConfig] = preferredServerStr

	// When this Endpoint is in "preferred-server" mode a bogus timestamp is inserted in the BackendConfig,
	// forcing the resynchronization of the object to the broker, which will indicate all clients that the
	// server has been restarted, and that they must re-connect to the endpoint.
	if preferredServer {
		backendConfig[submv1.PreferredServerConfig+"-timestamp"] = strconv.FormatInt(time.Now().UTC().Unix(), 10)
	}

	return backendConfig, nil
}

func getNodeBackendConfig(nodeObj *v1.Node) (map[string]string, error) {
	backendConfig := map[string]string{}
	if err := addConfigFrom(nodeObj.Name, nodeObj.Labels, backendConfig, ""); err != nil {
		return backendConfig, err
	}

	if err := addConfigFrom(nodeObj.Name, nodeObj.Annotations, backendConfig,
		"label %s=%s is overwritten by annotation with value %s"); err != nil {
		return backendConfig, err
	}

	return backendConfig, nil
}

func addConfigFrom(nodeName string, configs, backendConfig map[string]string, warningDuplicate string) error {
	validConfigs := set.New(submv1.ValidGatewayNodeConfig...)

	for cfg, value := range configs {
		if strings.HasPrefix(cfg, submv1.GatewayConfigPrefix) {
			config := cfg[len(submv1.GatewayConfigPrefix):]
			if !validConfigs.Has(config) {
				return errors.Errorf("unknown config annotation %q on node %q", cfg, nodeName)
			}

			if oldValue, ok := backendConfig[config]; ok && warningDuplicate != "" {
				logger.Warningf(warningDuplicate, cfg, oldValue, value)
			}

			backendConfig[config] = value
		}
	}

	return nil
}

// TODO: to handle de-duplication of code/finding common parts with the route agent.
func getCNIInterfaceIPAddress(clusterCIDRs []string) (string, error) {
	for _, clusterCIDR := range clusterCIDRs {
		_, clusterNetwork, err := net.ParseCIDR(clusterCIDR)
		if err != nil {
			return "", errors.Wrapf(err, "unable to ParseCIDR %q", clusterCIDR)
		}

		hostInterfaces, err := net.Interfaces()
		if err != nil {
			return "", errors.Wrap(err, "net.Interfaces() returned error")
		}

		for _, iface := range hostInterfaces {
			addrs, err := iface.Addrs()
			if err != nil {
				return "", errors.Wrapf(err, "for interface %q, iface.Addrs returned error", iface.Name)
			}

			for i := range addrs {
				ipAddr, _, err := net.ParseCIDR(addrs[i].String())
				if err != nil {
					logger.Error(nil, "Unable to ParseCIDR : %q", addrs[i].String())
				} else if ipAddr.To4() != nil {
					logger.V(log.DEBUG).Infof("Interface %q has %q address", iface.Name, ipAddr)
					address := net.ParseIP(ipAddr.String())

					// Verify that interface has an address from cluster CIDR.
					if clusterNetwork.Contains(address) {
						logger.V(log.DEBUG).Infof("Found CNI Interface %q that has IP %q from ClusterCIDR %q",
							iface.Name, ipAddr.String(), clusterCIDR)
						return ipAddr.String(), nil
					}
				}
			}
		}
	}

	return "", errors.Errorf("unable to find CNI Interface on the host which has IP from %q", clusterCIDRs)
}
