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

package util

import (
	"fmt"
	"strings"
	"unicode"

	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/types"
	"k8s.io/apimachinery/pkg/api/equality"
)

func GetClusterIDFromCableName(cableName string) string {
	// length is 11
	// 0           1    2   3    4    5       6   7  8 9  10
	// submariner-cable-my-super-long_cluster-id-172-16-32-5
	cableSplit := strings.Split(cableName, "-")
	clusterID := cableSplit[2]

	for i := 3; i < len(cableSplit)-4; i++ {
		clusterID = clusterID + "-" + cableSplit[i]
	}

	return clusterID
}

func GetEndpointCRDName(endpoint *types.SubmarinerEndpoint) (string, error) {
	// We'll panic if endpoint is nil, this is intentional
	return GetEndpointCRDNameFromParams(endpoint.Spec.ClusterID, endpoint.Spec.CableName)
}

func GetEndpointCRDNameFromParams(clusterID, cableName string) (string, error) {
	if clusterID == "" || cableName == "" {
		return "", fmt.Errorf("error, cluster ID or cable name was empty")
	}

	return EnsureValidName(fmt.Sprintf("%s-%s", clusterID, cableName)), nil
}

func GetClusterCRDName(cluster *types.SubmarinerCluster) (string, error) {
	// We'll panic if cluster is nil, this is intentional
	if cluster.Spec.ClusterID == "" {
		return "", fmt.Errorf("ClusterID was empty")
	}

	return cluster.Spec.ClusterID, nil
}

func CompareEndpointSpec(left, right *subv1.EndpointSpec) bool {
	if left == nil && right == nil {
		return true
	}

	if left == nil || right == nil {
		return false
	}

	// maybe we have to use just reflect.DeepEqual(left, right), but in this case the subnets order will influence.
	return left.ClusterID == right.ClusterID && left.CableName == right.CableName && left.Hostname == right.Hostname &&
		left.Backend == right.Backend && isBackendConfigSame(left, right)
}

func isBackendConfigSame(left, right *subv1.EndpointSpec) bool {
	if left.BackendConfig[subv1.UsingLoadBalancer] == "true" &&
		right.BackendConfig[subv1.UsingLoadBalancer] == "true" {
		// When Gateway pod comes up with loadbalancer mode enabled, it inserts a preferred-server-timestamp in
		// the BackendConfig when the Gateway pod comes up. So, in loadbalancer mode, we just have to compare
		// the load-balancer status.
		return true
	}

	return equality.Semantic.DeepEqual(left.BackendConfig, right.BackendConfig)
}

func EnsureValidName(name string) string {
	// K8s only allows lower case alphanumeric characters, '-' or '.'. Regex used for validation is
	// '[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*'
	return strings.Map(func(c rune) rune {
		c = unicode.ToLower(c)
		if !unicode.IsDigit(c) && !unicode.IsLower(c) && c != '-' && c != '.' {
			return '-'
		}

		return c
	}, name)
}
