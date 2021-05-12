/*
© 2021 Red Hat, Inc. and others

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
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/rdegges/go-ipify"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/stringset"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog"

	submv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/types"
)

func GetLocal(submSpec types.SubmarinerSpecification, privateIP string, gwNode *v1.Node) (types.SubmarinerEndpoint, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return types.SubmarinerEndpoint{}, fmt.Errorf("error getting hostname: %v", err)
	}

	var localSubnets []string

	globalnetEnabled := false

	if len(submSpec.GlobalCidr) > 0 {
		localSubnets = submSpec.GlobalCidr
		globalnetEnabled = true
	} else {
		localSubnets = append(localSubnets, submSpec.ServiceCidr...)
		localSubnets = append(localSubnets, submSpec.ClusterCidr...)
	}

	backendConfig, err := getBackendConfig(gwNode)
	if err != nil {
		return types.SubmarinerEndpoint{}, err
	}

	endpoint := types.SubmarinerEndpoint{
		Spec: submv1.EndpointSpec{
			CableName:     fmt.Sprintf("submariner-cable-%s-%s", submSpec.ClusterID, strings.ReplaceAll(privateIP, ".", "-")),
			ClusterID:     submSpec.ClusterID,
			Hostname:      hostname,
			PrivateIP:     privateIP,
			NATEnabled:    submSpec.NATEnabled,
			Subnets:       localSubnets,
			Backend:       submSpec.CableDriver,
			BackendConfig: backendConfig,
		},
	}

	publicIP, err := ipify.GetIp()
	if err != nil {
		return types.SubmarinerEndpoint{}, fmt.Errorf("could not determine public IP: %v", err)
	}

	endpoint.Spec.PublicIP = publicIP

	if !globalnetEnabled {
		// When globalnet is enabled, HealthCheckIP will be the globalIP assigned to the Active GatewayNode.
		// In a fresh deployment, globalIP annotation for the node might take few seconds. So we listen on NodeEvents
		// and update the endpoint HealthCheckIP (to globalIP) in datastoreSyncer at a later stage. This will trigger
		// the HealthCheck between the clusters.
		endpoint.Spec.HealthCheckIP, err = getCNIInterfaceIPAddress(submSpec.ClusterCidr)
		if err != nil {
			return types.SubmarinerEndpoint{}, fmt.Errorf("error getting CNI Interface IP address: %v", err)
		}
	}

	return endpoint, nil
}

func getBackendConfig(nodeObj *v1.Node) (map[string]string, error) {
	backendConfig, err := getLabelsBackendConfig(nodeObj, map[string]string{})
	if err != nil {
		return backendConfig, err
	}

	// Enable and publish the natt-discovery-port by default
	if _, ok := backendConfig[submv1.NATTDiscoveryPortConfig]; !ok {
		backendConfig[submv1.NATTDiscoveryPortConfig] = submv1.DefaultNATTDiscoveryPort
	}

	//TODO: we should allow the cable drivers to capture and expose BackendConfig settings, instead of doing
	//      it here.
	preferredServerStr := os.Getenv("CE_IPSEC_PREFERREDSERVER")
	if preferredServerStr == "" {
		preferredServerStr = "false"
	}

	preferredServer, err := strconv.ParseBool(preferredServerStr)
	if err != nil {
		return backendConfig, errors.Wrapf(err, "error parsing CE_IPSEC_PREFERREDSERVER bool: %s", preferredServerStr)
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

func getLabelsBackendConfig(nodeObj *v1.Node, backendConfig map[string]string) (map[string]string, error) {
	validConfigs := stringset.New(submv1.ValidGatewayNodeConfig...)

	for label, value := range nodeObj.Labels {
		if strings.HasPrefix(label, submv1.GatewayConfigLabelPrefix) {
			config := label[len(submv1.GatewayConfigLabelPrefix):]
			if !validConfigs.Contains(config) {
				return backendConfig, errors.Errorf("unknown config label %q on node %q", nodeObj.Name, label)
			}

			backendConfig[config] = value
		}
	}

	return backendConfig, nil
}

//TODO: to handle de-duplication of code/finding common parts with the route agent
func getCNIInterfaceIPAddress(clusterCIDRs []string) (string, error) {
	for _, clusterCIDR := range clusterCIDRs {
		_, clusterNetwork, err := net.ParseCIDR(clusterCIDR)
		if err != nil {
			return "", fmt.Errorf("unable to ParseCIDR %q : %v", clusterCIDR, err)
		}

		hostInterfaces, err := net.Interfaces()
		if err != nil {
			return "", fmt.Errorf("net.Interfaces() returned error : %v", err)
		}

		for _, iface := range hostInterfaces {
			addrs, err := iface.Addrs()
			if err != nil {
				return "", fmt.Errorf("for interface %q, iface.Addrs returned error: %v", iface.Name, err)
			}

			for i := range addrs {
				ipAddr, _, err := net.ParseCIDR(addrs[i].String())
				if err != nil {
					klog.Errorf("Unable to ParseCIDR : %q", addrs[i].String())
				} else if ipAddr.To4() != nil {
					klog.V(log.DEBUG).Infof("Interface %q has %q address", iface.Name, ipAddr)
					address := net.ParseIP(ipAddr.String())

					// Verify that interface has an address from cluster CIDR
					if clusterNetwork.Contains(address) {
						klog.V(log.DEBUG).Infof("Found CNI Interface %q that has IP %q from ClusterCIDR %q",
							iface.Name, ipAddr.String(), clusterCIDR)
						return ipAddr.String(), nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("unable to find CNI Interface on the host which has IP from %q", clusterCIDRs)
}
