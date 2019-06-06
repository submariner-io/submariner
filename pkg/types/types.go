package types

import (
	subv1 "github.com/rancher/submariner/pkg/apis/submariner.io/v1"
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
	ColorCodes  []string
	NatEnabled  bool
	Broker      string
}

type Secure struct {
	APIKey    string
	SecretKey string
}
