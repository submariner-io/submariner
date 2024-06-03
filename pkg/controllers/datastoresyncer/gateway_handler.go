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

package datastoresyncer

import (
	"context"
	"net"

	"github.com/submariner-io/admiral/pkg/resource"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func (d *DatastoreSyncer) handleCreateOrUpdateGateway(obj runtime.Object, _ int) bool {
	globalIP := resource.MustToMeta(obj).GetAnnotations()[constants.SmGlobalIP]

	// Validate that the global IP falls in the global CIDR allocated to the cluster.
	if globalIP != "" {
		_, ipnet, err := net.ParseCIDR(d.localCluster.Spec.GlobalCIDR[0])
		if err != nil {
			// Ideally this will not happen as globalCIDR is expected to be a valid CIDR.
			logger.Errorf(err, "Error parsing the GlobalCIDR %q", d.localCluster.Spec.GlobalCIDR)
			return false
		}

		if ipnet.Contains(net.ParseIP(globalIP)) {
			return d.updateLocalEndpointIfNecessary(globalIP)
		}
	}

	return false
}

func (d *DatastoreSyncer) areGatewaysEquivalent(obj1, obj2 *unstructured.Unstructured) bool {
	existingGlobalIP := obj1.GetAnnotations()[constants.SmGlobalIP]
	newGlobalIP := obj2.GetAnnotations()[constants.SmGlobalIP]

	if existingGlobalIP != newGlobalIP {
		logger.Infof("Global IP for node %q changed from %q to %q", obj1.GetName(), existingGlobalIP, newGlobalIP)
	}

	return existingGlobalIP == newGlobalIP
}

func (d *DatastoreSyncer) updateLocalEndpointIfNecessary(globalIP string) bool {
	spec := d.localEndpoint.Spec()
	if spec.HealthCheckIP != globalIP {
		logger.Infof("Updating the endpoint HealthCheckIP to globalIP %q", globalIP)

		err := d.localEndpoint.Update(context.TODO(), func(existing *submarinerv1.EndpointSpec) {
			existing.HealthCheckIP = globalIP
		})
		if err != nil {
			logger.Warningf("Error updating the local submariner Endpoint with HealthcheckIP %s: %v", globalIP, err)
			return true
		}
	}

	return false
}
