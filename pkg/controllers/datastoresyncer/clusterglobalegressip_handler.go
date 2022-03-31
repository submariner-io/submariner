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
	"reflect"
	"strings"

	"github.com/submariner-io/admiral/pkg/log"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
)

func (d *DatastoreSyncer) handleCreateOrUpdateClusterGlobalEgressIP(obj runtime.Object, numRequeues int) bool {
	clusterGlobalEgressIP := obj.(*submarinerv1.ClusterGlobalEgressIP)
	klog.V(log.DEBUG).Infof("Update called for globalegressIP %s datastoreSyncer's Hostname %s",
		clusterGlobalEgressIP.ObjectMeta.Name, d.localNodeName)

	if clusterGlobalEgressIP.Name != constants.ClusterGlobalEgressIPName {
		if d.localNodeName != strings.TrimSuffix(clusterGlobalEgressIP.Name, "-"+constants.ClusterGlobalEgressIPName) {
			return false
		}
	}

	if !reflect.DeepEqual(clusterGlobalEgressIP.Status.AllocatedIPs, d.localEndpoint.Spec.AllocatedIPs) {
		klog.Infof("Updating the endpoint AllocatedIPs to %v", clusterGlobalEgressIP.Status.AllocatedIPs)

		// Save the original list
		existingAllocatedIPs := make([]string, len(d.localEndpoint.Spec.AllocatedIPs))
		copy(existingAllocatedIPs, d.localEndpoint.Spec.AllocatedIPs)

		d.localEndpoint.Spec.AllocatedIPs = nil
		d.localEndpoint.Spec.AllocatedIPs = make([]string, len(clusterGlobalEgressIP.Status.AllocatedIPs))
		copy(d.localEndpoint.Spec.AllocatedIPs, clusterGlobalEgressIP.Status.AllocatedIPs)

		if err := d.createOrUpdateLocalEndpoint(); err != nil {
			klog.Warningf("Error updating the local submariner Endpoint with AllocatedIPs: %v", err)

			d.localEndpoint.Spec.AllocatedIPs = nil
			d.localEndpoint.Spec.AllocatedIPs = make([]string, len(existingAllocatedIPs))
			copy(d.localEndpoint.Spec.AllocatedIPs, existingAllocatedIPs)

			return true
		}
	}

	return false
}

func (d *DatastoreSyncer) handleDeleteClusterGlobalEgressIP(obj runtime.Object, numRequeues int) bool {
	clusterGlobalEgressIP := obj.(*submarinerv1.ClusterGlobalEgressIP)

	if clusterGlobalEgressIP.Name != constants.ClusterGlobalEgressIPName {
		if !strings.HasPrefix(clusterGlobalEgressIP.Name, d.localNodeName) {
			return false
		}
	}

	if len(d.localEndpoint.Spec.AllocatedIPs) != 0 {
		klog.Info("Updating the endpoint remove all AllocatedIPs")

		existingAllocatedIPs := make([]string, len(d.localEndpoint.Spec.AllocatedIPs))
		copy(existingAllocatedIPs, d.localEndpoint.Spec.AllocatedIPs)

		d.localEndpoint.Spec.AllocatedIPs = nil

		if err := d.createOrUpdateLocalEndpoint(); err != nil {
			klog.Warningf("Error updating the local submariner Endpoint with no AllocatedIPs: %v", err)

			d.localEndpoint.Spec.AllocatedIPs = nil
			d.localEndpoint.Spec.AllocatedIPs = make([]string, len(existingAllocatedIPs))
			copy(d.localEndpoint.Spec.AllocatedIPs, existingAllocatedIPs)

			return true
		}
	}

	return false
}
