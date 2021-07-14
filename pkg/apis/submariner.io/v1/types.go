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
	"net"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:noStatus
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Cluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ClusterSpec `json:"spec"`
}

type ClusterSpec struct {
	ClusterID   string   `json:"cluster_id"` // perhaps this could just be a hash of the name...?
	ColorCodes  []string `json:"color_codes"`
	ServiceCIDR []string `json:"service_cidr"`
	ClusterCIDR []string `json:"cluster_cidr"`
	GlobalCIDR  []string `json:"global_cidr"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Cluster `json:"items"`
}

// +genclient
// +genclient:noStatus
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Endpoint struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              EndpointSpec `json:"spec"`
}

func (ep *Endpoint) GatewayIP() net.IP {
	if ep.Spec.PublicIP != "" {
		return net.ParseIP(ep.Spec.PublicIP)
	} else {
		return net.ParseIP(ep.Spec.PrivateIP)
	}
}

type EndpointSpec struct {
	ClusterID string `json:"cluster_id"`
	CableName string `json:"cable_name"`
	// +optional
	HealthCheckIP string            `json:"healthCheckIP,omitempty"`
	Hostname      string            `json:"hostname"`
	Subnets       []string          `json:"subnets"`
	PrivateIP     string            `json:"private_ip"`
	PublicIP      string            `json:"public_ip"`
	NATEnabled    bool              `json:"nat_enabled"`
	Backend       string            `json:"backend"`
	BackendConfig map[string]string `json:"backend_config,omitempty"`
}

const (
	GatewayConfigPrefix     = "gateway.submariner.io/"
	UDPPortConfig           = "udp-port"
	NATTDiscoveryPortConfig = "natt-discovery-port"
	PreferredServerConfig   = "preferred-server"
	PublicIP                = "public-ip"
	UsingLoadBalancer       = "using-loadbalancer"
)

const (
	DefaultNATTDiscoveryPort = "4490"
)

// Valid PublicIP resolvers.
const (
	IPv4         = "ipv4" // ipv4:1.2.3.4
	LoadBalancer = "lb"   // lb:external-gw-lb
	API          = "api"  // api:api.ipify.org
	DNS          = "dns"  // dns:mygateway.dns.name.com
)

// ValidGatewayNodeConfig list should contain only keys that configure node specific settings via labels
var ValidGatewayNodeConfig = []string{
	UDPPortConfig,
	NATTDiscoveryPortConfig,
	PublicIP,
	PreferredServerConfig,
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type EndpointList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Endpoint `json:"items"`
}

// +genclient
// +genclient:noStatus
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:printcolumn:JSONPath=".status.haStatus",name="HA Status",description="High availability status of the Gateway",type="string"

type Gateway struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Status            GatewayStatus `json:"status"`
}

type GatewayStatus struct {
	Version       string       `json:"version"`
	HAStatus      HAStatus     `json:"haStatus"`
	LocalEndpoint EndpointSpec `json:"localEndpoint"`
	StatusFailure string       `json:"statusFailure"`
	Connections   []Connection `json:"connections"`
}

// LatencySpec describes the round trip time information for a packet
// between the gateway pods of two clusters.
type LatencyRTTSpec struct {
	Last    string `json:"last,omitempty"`
	Min     string `json:"min,omitempty"`
	Average string `json:"average,omitempty"`
	Max     string `json:"max,omitempty"`
	StdDev  string `json:"stdDev,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type GatewayList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Gateway `json:"items"`
}

type HAStatus string

const (
	HAStatusActive  HAStatus = "active"
	HAStatusPassive HAStatus = "passive"
)

type Connection struct {
	Status        ConnectionStatus `json:"status"`
	StatusMessage string           `json:"statusMessage"`
	Endpoint      EndpointSpec     `json:"endpoint"`
	UsingIP       string           `json:"usingIP,omitempty"`
	UsingNAT      bool             `json:"usingNAT,omitempty"`
	// +optional
	LatencyRTT *LatencyRTTSpec `json:"latencyRTT,omitempty"`
}

type ConnectionStatus string

const (
	Connected       ConnectionStatus = "connected"
	Connecting      ConnectionStatus = "connecting"
	ConnectionError ConnectionStatus = "error"
)

func NewConnection(endpointSpec EndpointSpec, usedIP string, nat bool) *Connection {
	return &Connection{Endpoint: endpointSpec, UsingIP: usedIP, UsingNAT: nat}
}

func (c *Connection) SetStatus(status ConnectionStatus, messageFormat string, a ...interface{}) {
	c.Status = status
	c.StatusMessage = fmt.Sprintf(messageFormat, a...)
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:shortName="geip"

// GlobalEgressIP defines a policy for allocating GlobalIPs for selected pods in the namespace of the GlobalEgressIP object.
type GlobalEgressIP struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is the specification of the desired behavior.
	Spec GlobalEgressIPSpec `json:"spec"`

	// The most recently observed status. Read-only.
	// +optional
	Status GlobalEgressIPStatus `json:"status,omitempty"`
}

type GlobalEgressIPSpec struct {
	// The requested number of contiguous GlobalIPs to allocate from the Globalnet CIDR assigned to the cluster.
	// If not specified, defaults to 1.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=20
	// +optional
	NumberOfIPs *int `json:"numberOfIPs,omitempty"`

	// Selects specific pods in the namespace of this GlobalEgressIP to which this GlobalEgressIP applies. If not specified,
	// all pods in the namespace are selected.
	// If a pod matches multiple GlobalEgressIP objects, there is no guarantee from which GlobalEgressIP its
	// GlobalIP will be assigned.
	// +optional
	PodSelector *metav1.LabelSelector `json:"podSelector,omitempty"`
}

type GlobalEgressIPConditionType string

const (
	GlobalEgressIPAllocated GlobalEgressIPConditionType = "Allocated"
	GlobalEgressIPUpdated   GlobalEgressIPConditionType = "Updated"
)

type GlobalEgressIPStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// The list of allocated GlobalIPs.
	// +optional
	AllocatedIPs []string `json:"allocatedIPs,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type GlobalEgressIPList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []GlobalEgressIP `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:scope="Cluster",shortName="cgeip"
// ClusterGlobalEgressIP defines a policy for allocating GlobalIPs at the cluster level to be used when no GlobalEgressIP
// applies.
type ClusterGlobalEgressIP struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is the specification of desired behavior.
	Spec ClusterGlobalEgressIPSpec `json:"spec"`

	// The most recently observed status. Read-only.
	// +optional
	Status GlobalEgressIPStatus `json:"status,omitempty"`
}

type ClusterGlobalEgressIPSpec struct {
	// The requested number of contiguous GlobalIPs to allocate from the Globalnet CIDR assigned to the cluster.
	// If not specified, defaults to 1.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=20
	// +optional
	NumberOfIPs *int `json:"numGlobalIPs,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ClusterGlobalEgressIPList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []ClusterGlobalEgressIP `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:shortName="giip"
// +kubebuilder:printcolumn:JSONPath=".status.allocatedIP",name="IP",description="Global IP Allocated",type="string"

type GlobalIngressIP struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is the specification of desired behavior of GlobalIngressIP object.
	Spec GlobalIngressIPSpec `json:"spec"`

	// Observed status of GlobalIngressIP. Its a read-only field.
	// +optional
	Status GlobalIngressIPStatus `json:"status,omitempty"`
}

type GlobalIngressIPSpec struct {
	// Specifies the type of the entity targeted by this object.
	Target TargetType `json:"target"`

	// The reference to a targeted Service, if applicable.
	// +Optional
	ServiceRef *corev1.LocalObjectReference `json:"serviceRef,omitempty"`

	// The reference to a targeted Pod, if applicable.
	// +Optional
	PodRef *corev1.LocalObjectReference `json:"podRef,omitempty"`
}

type TargetType string

const (
	ClusterIPService   TargetType = "ClusterIPService"
	HeadlessServicePod TargetType = "HeadlessServicePod"
)

type GlobalIngressIPStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// The GlobalIP allocated to this object.
	// +optional
	AllocatedIP string `json:"allocatedIP"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type GlobalIngressIPList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []GlobalIngressIP `json:"items"`
}
