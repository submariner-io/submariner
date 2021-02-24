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
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:noStatus
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type GlobalnetEgressIP struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is the specification of desired behavior of GlobalnetEgressIP object
	Spec EgressIPSpec `json:"spec"`

	// Observed status of GlobalnetEgressIP. Its a read-only field.
	// +optional
	Status EgressIPStatus `json:"status,omitempty"`
}

type EgressIPSpec struct {
	// User can explicitly request a specific GlobalIP and this should be a valid IPaddress
	// from globalnetCIDR allocated to the Cluster. If the requested globalIP falls outside
	// of the globalnetCIDR or is already allocated (either fully or partially), then Status
	// field will be set to Error with appropriate message.
	// If this field is not specified, a single globalIP will be allocated.
	// This field cannot be changed through updates.
	// +optional
	GlobalIP string `json:"globalIP,omitempty"`

	// The number of globalIP's requested. Globalnet Controller will allocate the requested
	// number of contiguous GlobalIPs for this GlobalnetEgressIP object.
	// User can specify this field while omitting GlobalIP. In such cases, Globalnet will
	// auto-allocate the requested number of contiguous GlobalIPs.
	// If unspecified, NumGlobalIPs defaults to 1.
	// +optional
	NumGlobalIPs int `json:"numGlobalIPs,omitempty"`

	// Selects the Namespaces to which this GlobalnetEgressIP object applies.
	// If an empty namespaceSelector: {} is configured, it selects all namespaces.
	// If an empty namespaceSelector: {} is configured along with empty PodSelector, it applies
	// to whole Cluster.
	// On the other hand, if you omit specifying namespaceSelector, it does not select any
	// namespaces and selects only Pods from the namespace where the GlobalnetEgressIP is
	// deployed to.
	// +optional
	NamespaceSelector metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// Selects the pods whose label match the definition. This is an optional field and
	// in case it is not set, it results in all the Pods selected from the NamespaceSelector.
	// In case it is set, its intersected with NamespaceSelector, and only Pods that match the
	// PodSelector and present in the NamespaceSelector will have the GlobalIPs as EgressIPs.
	// Either namespaceSelector or podSelector have to be specified. If both are omitted,
	// its considered as an error.
	// When a Pod or Namespace matches multiple GlobalnetEgressIP objects, there is no guarantee
	// which of the globalIP address that are assigned to GlobalnetEgressIP will be used.
	// +optional
	PodSelector metav1.LabelSelector `json:"podSelector,omitempty"`
}

// GlobalnetEgressIPStatus identifies the status of GlobalnetEgressIP request.
type GlobalnetEgressIPStatus string

const (
	// GlobalnetEgressIPSuccess means that Globalnet was able to successfully
	// allocate the GlobalIP as requested in GlobalnetEgressIP object.
	GlobalnetEgressIPSuccess GlobalnetEgressIPStatus = "Success"

	// GlobalnetEgressIPError means that Globalnet was unable to allocate the
	// requested GlobalIP.
	GlobalnetEgressIPError GlobalnetEgressIPStatus = "Error"
)

type EgressIPStatus struct {
	// Status is one of {"Success", "Error"}
	Status GlobalnetEgressIPStatus `json:"status,omitempty"`

	// +optional
	Message *string `json:"message,omitempty"`

	// The list of GlobalIPs assigned via this GlobalnetEgressIP object.
	GlobalIPs []string `json:"globalIPs"`

	// The Namespaces to which the GlobalIPs are applied.
	Namespaces []string `json:"namespaces"`

	// The Pods to which the GlobalIPs are applied.
	Pods []string `json:"pods"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type GlobalnetEgressIPList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []GlobalnetEgressIP `json:"items"`
}
