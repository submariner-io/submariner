package libreswan

import (
	"fmt"

	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/types"
)

const (
	cableDriverName = "libreswan"
)

func init() {
	cable.AddDriver(cableDriverName, NewLibreSwan)
}

type libreSwan struct {
}

// NewLibreSwan starts an IKE daemon using LibreSwan and configures it to manage Submariner's endpoints
func NewLibreSwan(localSubnets []string, localEndpoint types.SubmarinerEndpoint, localCluster types.SubmarinerCluster) (cable.Driver, error) {
	return &libreSwan{}, nil
}

// GetName returns driver's name
func (i *libreSwan) GetName() string {
	return cableDriverName
}

// Init initializes the driver with any state it needs.
func (i *libreSwan) Init() error {
	return nil
}

// GetActiveConnections returns an array of all the active connections for the given cluster.
func (i *libreSwan) GetActiveConnections(clusterID string) ([]string, error) {
	return make([]string, 0), nil
}

// GetConnections() returns an array of the existing connections, including status and endpoint info
func (i *libreSwan) GetConnections() (*[]v1.Connection, error) {
	connections := make([]v1.Connection, 0)
	return &connections, nil
}

// ConnectToEndpoint establishes a connection to the given endpoint and returns a string
// representation of the IP address of the target endpoint.
func (i *libreSwan) ConnectToEndpoint(endpoint types.SubmarinerEndpoint) (string, error) {
	return "", fmt.Errorf("Not implemented")
}

// DisconnectFromEndpoint disconnects from the connection to the given endpoint.
func (i *libreSwan) DisconnectFromEndpoint(endpoint types.SubmarinerEndpoint) error {
	return fmt.Errorf("Not implemented")
}
