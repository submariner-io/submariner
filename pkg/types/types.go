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
	ClusterCidr                   []string
	ColorCodes                    []string
	GlobalCidr                    []string
	ServiceCidr                   []string
	Broker                        string
	CableDriver                   string
	ClusterID                     string
	Namespace                     string
	Token                         string
	Debug                         bool
	NatEnabled                    bool
	HealthCheckEnabled            bool
	HealthCheckInterval           uint
	HealthCheckMaxPacketLossCount uint
}

type Secure struct {
	APIKey    string
	SecretKey string
}
