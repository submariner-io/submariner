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

import (
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

// GatewayStatusApplyConfiguration represents a declarative configuration of the GatewayStatus type for use
// with apply.
type GatewayStatusApplyConfiguration struct {
	Version       *string                         `json:"version,omitempty"`
	HAStatus      *v1.HAStatus                    `json:"haStatus,omitempty"`
	LocalEndpoint *EndpointSpecApplyConfiguration `json:"localEndpoint,omitempty"`
	StatusFailure *string                         `json:"statusFailure,omitempty"`
	Connections   []ConnectionApplyConfiguration  `json:"connections,omitempty"`
}

// GatewayStatusApplyConfiguration constructs a declarative configuration of the GatewayStatus type for use with
// apply.
func GatewayStatus() *GatewayStatusApplyConfiguration {
	return &GatewayStatusApplyConfiguration{}
}

// WithVersion sets the Version field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Version field is set to the value of the last call.
func (b *GatewayStatusApplyConfiguration) WithVersion(value string) *GatewayStatusApplyConfiguration {
	b.Version = &value
	return b
}

// WithHAStatus sets the HAStatus field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the HAStatus field is set to the value of the last call.
func (b *GatewayStatusApplyConfiguration) WithHAStatus(value v1.HAStatus) *GatewayStatusApplyConfiguration {
	b.HAStatus = &value
	return b
}

// WithLocalEndpoint sets the LocalEndpoint field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the LocalEndpoint field is set to the value of the last call.
func (b *GatewayStatusApplyConfiguration) WithLocalEndpoint(value *EndpointSpecApplyConfiguration) *GatewayStatusApplyConfiguration {
	b.LocalEndpoint = value
	return b
}

// WithStatusFailure sets the StatusFailure field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the StatusFailure field is set to the value of the last call.
func (b *GatewayStatusApplyConfiguration) WithStatusFailure(value string) *GatewayStatusApplyConfiguration {
	b.StatusFailure = &value
	return b
}

// WithConnections adds the given value to the Connections field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Connections field.
func (b *GatewayStatusApplyConfiguration) WithConnections(values ...*ConnectionApplyConfiguration) *GatewayStatusApplyConfiguration {
	for i := range values {
		if values[i] == nil {
			panic("nil value passed to WithConnections")
		}
		b.Connections = append(b.Connections, *values[i])
	}
	return b
}
