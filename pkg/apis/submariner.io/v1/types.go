package v1

import (
	"net"

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
	ClusterID     string            `json:"cluster_id"`
	CableName     string            `json:"cable_name"`
	Hostname      string            `json:"hostname"`
	Subnets       []string          `json:"subnets"`
	PrivateIP     net.IP            `json:"private_ip"`
	PublicIP      net.IP            `json:"public_ip"`
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
