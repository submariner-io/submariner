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
	corev1 "k8s.io/api/core/v1"
)

// GlobalIngressIPSpecApplyConfiguration represents a declarative configuration of the GlobalIngressIPSpec type for use
// with apply.
type GlobalIngressIPSpecApplyConfiguration struct {
	Target     *v1.TargetType               `json:"target,omitempty"`
	ServiceRef *corev1.LocalObjectReference `json:"serviceRef,omitempty"`
	PodRef     *corev1.LocalObjectReference `json:"podRef,omitempty"`
}

// GlobalIngressIPSpecApplyConfiguration constructs a declarative configuration of the GlobalIngressIPSpec type for use with
// apply.
func GlobalIngressIPSpec() *GlobalIngressIPSpecApplyConfiguration {
	return &GlobalIngressIPSpecApplyConfiguration{}
}

// WithTarget sets the Target field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Target field is set to the value of the last call.
func (b *GlobalIngressIPSpecApplyConfiguration) WithTarget(value v1.TargetType) *GlobalIngressIPSpecApplyConfiguration {
	b.Target = &value
	return b
}

// WithServiceRef sets the ServiceRef field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ServiceRef field is set to the value of the last call.
func (b *GlobalIngressIPSpecApplyConfiguration) WithServiceRef(value corev1.LocalObjectReference) *GlobalIngressIPSpecApplyConfiguration {
	b.ServiceRef = &value
	return b
}

// WithPodRef sets the PodRef field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the PodRef field is set to the value of the last call.
func (b *GlobalIngressIPSpecApplyConfiguration) WithPodRef(value corev1.LocalObjectReference) *GlobalIngressIPSpecApplyConfiguration {
	b.PodRef = &value
	return b
}
