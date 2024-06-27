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

// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

// EndpointSpecApplyConfiguration represents an declarative configuration of the EndpointSpec type for use
// with apply.
type EndpointSpecApplyConfiguration struct {
	ClusterID     *string           `json:"cluster_id,omitempty"`
	CableName     *string           `json:"cable_name,omitempty"`
	HealthCheckIP *string           `json:"healthCheckIP,omitempty"`
	Hostname      *string           `json:"hostname,omitempty"`
	Subnets       []string          `json:"subnets,omitempty"`
	PrivateIP     *string           `json:"private_ip,omitempty"`
	PublicIP      *string           `json:"public_ip,omitempty"`
	NATEnabled    *bool             `json:"nat_enabled,omitempty"`
	Backend       *string           `json:"backend,omitempty"`
	BackendConfig map[string]string `json:"backend_config,omitempty"`
}

// EndpointSpecApplyConfiguration constructs an declarative configuration of the EndpointSpec type for use with
// apply.
func EndpointSpec() *EndpointSpecApplyConfiguration {
	return &EndpointSpecApplyConfiguration{}
}

// WithClusterID sets the ClusterID field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ClusterID field is set to the value of the last call.
func (b *EndpointSpecApplyConfiguration) WithClusterID(value string) *EndpointSpecApplyConfiguration {
	b.ClusterID = &value
	return b
}

// WithCableName sets the CableName field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the CableName field is set to the value of the last call.
func (b *EndpointSpecApplyConfiguration) WithCableName(value string) *EndpointSpecApplyConfiguration {
	b.CableName = &value
	return b
}

// WithHealthCheckIP sets the HealthCheckIP field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the HealthCheckIP field is set to the value of the last call.
func (b *EndpointSpecApplyConfiguration) WithHealthCheckIP(value string) *EndpointSpecApplyConfiguration {
	b.HealthCheckIP = &value
	return b
}

// WithHostname sets the Hostname field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Hostname field is set to the value of the last call.
func (b *EndpointSpecApplyConfiguration) WithHostname(value string) *EndpointSpecApplyConfiguration {
	b.Hostname = &value
	return b
}

// WithSubnets adds the given value to the Subnets field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Subnets field.
func (b *EndpointSpecApplyConfiguration) WithSubnets(values ...string) *EndpointSpecApplyConfiguration {
	for i := range values {
		b.Subnets = append(b.Subnets, values[i])
	}
	return b
}

// WithPrivateIP sets the PrivateIP field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the PrivateIP field is set to the value of the last call.
func (b *EndpointSpecApplyConfiguration) WithPrivateIP(value string) *EndpointSpecApplyConfiguration {
	b.PrivateIP = &value
	return b
}

// WithPublicIP sets the PublicIP field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the PublicIP field is set to the value of the last call.
func (b *EndpointSpecApplyConfiguration) WithPublicIP(value string) *EndpointSpecApplyConfiguration {
	b.PublicIP = &value
	return b
}

// WithNATEnabled sets the NATEnabled field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the NATEnabled field is set to the value of the last call.
func (b *EndpointSpecApplyConfiguration) WithNATEnabled(value bool) *EndpointSpecApplyConfiguration {
	b.NATEnabled = &value
	return b
}

// WithBackend sets the Backend field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Backend field is set to the value of the last call.
func (b *EndpointSpecApplyConfiguration) WithBackend(value string) *EndpointSpecApplyConfiguration {
	b.Backend = &value
	return b
}

// WithBackendConfig puts the entries into the BackendConfig field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, the entries provided by each call will be put on the BackendConfig field,
// overwriting an existing map entries in BackendConfig field with the same key.
func (b *EndpointSpecApplyConfiguration) WithBackendConfig(entries map[string]string) *EndpointSpecApplyConfiguration {
	if b.BackendConfig == nil && len(entries) > 0 {
		b.BackendConfig = make(map[string]string, len(entries))
	}
	for k, v := range entries {
		b.BackendConfig[k] = v
	}
	return b
}
