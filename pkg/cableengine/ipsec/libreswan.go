package ipsec

import (
	"fmt"

	"github.com/submariner-io/submariner/pkg/types"
)

type libreSwan struct {
}

// NewLibreSwan starts an IKE daemon using LibreSwan and configures it to manage Submariner's endpoints
func NewLibreSwan(localSubnets []string, localEndpoint types.SubmarinerEndpoint) (Driver, error) {
	return &libreSwan{}, nil
}

func (i *libreSwan) Init() error {
	return nil
}

func (i *libreSwan) GetActiveConnections(clusterID string) ([]string, error) {
	return make([]string, 0), nil
}

func (i *libreSwan) ConnectToEndpoint(endpoint types.SubmarinerEndpoint) (string, error) {
	return "", fmt.Errorf("Not implemented")
}

func (i *libreSwan) DisconnectFromEndpoint(endpoint types.SubmarinerEndpoint) error {
	return fmt.Errorf("Not implemented")
}
