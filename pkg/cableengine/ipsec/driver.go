package ipsec

import (
	"github.com/submariner-io/submariner/pkg/types"
)

// Driver is used by the ipsec engine to actually connect the tunnels.
type Driver interface {

	// Init initializes the driver with any state it needs.
	Init() error

	// GetActiveConnections returns an array of all the active connections for the given cluster.
	GetActiveConnections(clusterID string) ([]string, error)

	// ConnectToEndpoint establishes a connection to the given endpoint and returns a string
	// representation of the IP address of the target endpoint.
	ConnectToEndpoint(endpoint types.SubmarinerEndpoint) (string, error)

	// DisconnectFromEndpoint disconnects from the endpoint identified by the cable ID.
	DisconnectFromEndpoint(cableID string) error
}
