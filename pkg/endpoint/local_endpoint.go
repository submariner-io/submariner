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
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/util"
	submv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cidr"
	"github.com/submariner-io/submariner/pkg/cni"
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
	mutex     sync.Mutex
	spec      submv1.EndpointSpec
	created   bool
	endpoints dynamic.ResourceInterface
}

func NewLocal(spec *submv1.EndpointSpec, dynClient dynamic.Interface, namespace string) *Local {
	return &Local{
		spec:      *spec.DeepCopy(),
		endpoints: dynClient.Resource(submv1.EndpointGVR).Namespace(namespace),
	}
}

func (l *Local) Spec() *submv1.EndpointSpec {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	return l.spec.DeepCopy()
}

func (l *Local) Resource() *submv1.Endpoint {
	return endpointFrom(l.Spec())
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
	l.mutex.Lock()
	defer l.mutex.Unlock()

	var (
		newSpec *submv1.EndpointSpec
		err     error
	)

	if l.created {
		toUpdate := resource.MustToUnstructured(endpointFrom(&l.spec))

		err = util.MustUpdate[*unstructured.Unstructured](ctx, resource.ForDynamic(l.endpoints), toUpdate,
			func(existing *unstructured.Unstructured) (*unstructured.Unstructured, error) {
				ep := resource.MustFromUnstructured(existing, &submv1.Endpoint{})
				mutate(&ep.Spec)
				newSpec = &ep.Spec

				return resource.MustToUnstructured(ep), nil
			})
	} else {
		newSpec = &l.spec
		mutate(newSpec)
	}

	if err == nil {
		l.spec = *newSpec
	}

	return err
}

func (l *Local) Create(ctx context.Context) error {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	toCreate := resource.MustToUnstructured(endpointFrom(&l.spec))

	_, err := util.CreateOrUpdate[*unstructured.Unstructured](ctx, resource.ForDynamic(l.endpoints), toCreate,
		func(obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
			return util.CopyImmutableMetadata(obj, toCreate), nil
		})

	l.created = err == nil

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
		cniIface, err := cni.Discover(submSpec.ClusterCidr)
		if err != nil {
			return nil, fmt.Errorf("error getting CNI Interface IP address: %w."+
				"Please disable the health check if your CNI does not expose a pod IP on the nodes", err)
		}

		endpointSpec.HealthCheckIP = cniIface.IPAddress
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
