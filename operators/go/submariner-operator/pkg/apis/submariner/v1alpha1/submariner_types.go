package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SubmarinerSpec defines the desired state of Submariner
// +k8s:openapi-gen=true
type SubmarinerSpec struct {
	Version                  string `json:"version,omitempty"`
	Repository               string `json:"repository,omitempty"`
	CeIPSecNATTPort          int    `json:"ceIPSecNATTPort,omitempty"`
	CeIPSecIKEPort           int    `json:"ceIPSecIKEPort,omitempty"`
	CeIPSecDebug             bool   `json:"ceIPSecDebug"`
	CeIPSecPSK               string `json:"ceIPSecPSK"`
	BrokerK8sCA              string `json:"brokerK8sCA"`
	BrokerK8sRemoteNamespace string `json:"brokerK8sRemoteNamespace"`
	BrokerK8sApiServerToken  string `json:"brokerK8sApiServerToken"`
	BrokerK8sApiServer       string `json:"brokerK8sApiServer"`
	Broker                   string `json:"broker"`
	NatEnabled               bool   `json:"natEnabled"`
	Debug                    bool   `json:"debug"`
	ColorCodes               string `json:"colorCodes,omitempty"`
	ClusterID                string `json:"clusterID"`
	ServiceCIDR              string `json:"serviceCIDR"`
	ClusterCIDR              string `json:"clusterCIDR"`
	Namespace                string `json:"namespace"`
	Count                    int32  `json:"count"`
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
}

// SubmarinerStatus defines the observed state of Submariner
// +k8s:openapi-gen=true
type SubmarinerStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Submariner is the Schema for the submariners API
// +k8s:openapi-gen=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=submariners,scope=Namespaced
type Submariner struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SubmarinerSpec   `json:"spec,omitempty"`
	Status SubmarinerStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SubmarinerList contains a list of Submariner
type SubmarinerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Submariner `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Submariner{}, &SubmarinerList{})
}
