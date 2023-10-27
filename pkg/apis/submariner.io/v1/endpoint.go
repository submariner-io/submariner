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

package v1

import (
	"fmt"
	"strconv"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/resource"
	"k8s.io/apimachinery/pkg/api/equality"
)

func (ep *EndpointSpec) GetBackendPort(configName string, defaultValue int32) (int32, error) {
	if portStr := ep.BackendConfig[configName]; portStr != "" {
		port, err := parsePort(portStr)
		if err != nil {
			return defaultValue, errors.Wrapf(err, "error parsing backend config %s", configName)
		}

		return port, nil
	}

	return defaultValue, nil
}

func (ep *EndpointSpec) GetBackendBool(configName string, defaultValue *bool) (*bool, error) {
	if boolStr := ep.BackendConfig[configName]; boolStr != "" {
		boolValue, err := strconv.ParseBool(boolStr)
		if err != nil {
			return defaultValue, errors.Wrapf(err, "error parsing backend config %s", configName)
		}

		return &boolValue, nil
	}

	return defaultValue, nil
}

func parsePort(port string) (int32, error) {
	portInt, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return -1, errors.Wrapf(err, "error parsing port %s", port)
	} else if portInt < 1 {
		return -1, errors.Errorf("port %s is < 1", port)
	} else if portInt > 65535 {
		return -1, errors.Errorf("port %s is > 65535", port)
	}

	return int32(portInt), nil
}

func (ep *EndpointSpec) GenerateName() (string, error) {
	if ep.ClusterID == "" {
		return "", fmt.Errorf("ClusterID cannot be empty")
	}

	if ep.CableName == "" {
		return "", fmt.Errorf("CableName cannot be empty")
	}

	return resource.EnsureValidName(fmt.Sprintf("%s-%s", ep.ClusterID, ep.CableName)), nil
}

func (ep *EndpointSpec) Equals(other *EndpointSpec) bool {
	if ep == nil && other == nil {
		return true
	}

	if ep == nil || other == nil {
		return false
	}

	return ep.ClusterID == other.ClusterID && ep.CableName == other.CableName && ep.Hostname == other.Hostname &&
		ep.Backend == other.Backend && ep.hasSameBackendConfig(other)
}

func (ep *EndpointSpec) hasSameBackendConfig(other *EndpointSpec) bool {
	if ep.BackendConfig[UsingLoadBalancer] == "true" &&
		other.BackendConfig[UsingLoadBalancer] == "true" {
		// When Gateway pod comes up with loadbalancer mode enabled, it inserts a preferred-server-timestamp in
		// the BackendConfig when the Gateway pod comes up. So, in loadbalancer mode, we just have to compare
		// the load-balancer status.
		return true
	}

	return equality.Semantic.DeepEqual(ep.BackendConfig, other.BackendConfig)
}
