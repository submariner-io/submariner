package types

import (
	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

type SubmarinerCluster struct {
	ID   string            `json:"id"`
	Spec subv1.ClusterSpec `json:"spec"`
}

type SubmarinerEndpoint struct {
	Spec subv1.EndpointSpec `json:"spec"`
}

type SubmarinerSpecification struct {
	Namespace   string
	Debug       bool
	ClusterID   string
	Token       string
	ClusterCidr []string
	ServiceCidr []string
	GlobalCidr  []string
	ColorCodes  []string
	NatEnabled  bool
	Broker      string
	CableDriver string
}

type Secure struct {
	APIKey    string
	SecretKey string
}
