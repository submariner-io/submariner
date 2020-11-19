package v1

import (
	"fmt"

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

// LatencySpec describes the round trip time information in miliseconds for a packet
// between the gateway pods of two clusters.
type LatencySpec struct {
	LastRTT    *float64 `json:"lastRTT"`
	MinRTT     *float64 `json:"minRTT"`
	AverageRTT *float64 `json:"averageRTT"`
	MaxRTT     *float64 `json:"maxRTT"`
	StdDevRTT  *float64 `json:"stddevRTT"`
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
	// +optional
	Latency *LatencySpec `json:"latency,omitempty"`
}

type ConnectionStatus string

const (
	Connected       ConnectionStatus = "connected"
	Connecting      ConnectionStatus = "connecting"
	ConnectionError ConnectionStatus = "error"
)

func NewConnection(endpointSpec EndpointSpec) *Connection {
	return &Connection{Endpoint: endpointSpec}
}

func (c *Connection) SetStatus(status ConnectionStatus, messageFormat string, a ...interface{}) {
	c.Status = status
	c.StatusMessage = fmt.Sprintf(messageFormat, a...)
}
