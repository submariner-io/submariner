package cable

import (
	"fmt"

	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"

	"github.com/submariner-io/submariner/pkg/types"
	"k8s.io/klog"
)

// Driver is used by the ipsec engine to actually connect the tunnels.
type Driver interface {

	// Init initializes the driver with any state it needs.
	Init() error

	// GetActiveConnections returns an array of all the active connections for the given cluster.
	GetActiveConnections(clusterID string) ([]string, error)

	// GetConnections() returns an array of the existing connections, including status and endpoint info
	GetConnections() (*[]v1.Connection, error)

	// ConnectToEndpoint establishes a connection to the given endpoint and returns a string
	// representation of the IP address of the target endpoint.
	ConnectToEndpoint(endpoint types.SubmarinerEndpoint) (string, error)

	// DisconnectFromEndpoint disconnects from the connection to the given endpoint.
	DisconnectFromEndpoint(endpoint types.SubmarinerEndpoint) error

	// GetName returns driver's name
	GetName() string
}

// Function prototype to create a new driver
type DriverCreateFunc func(localSubnets []string, localEndpoint types.SubmarinerEndpoint) (Driver, error)

// Static map of supported drivers
var drivers = map[string]DriverCreateFunc{}

// Default name of the cable driver
var defaultCableDriver string

// Adds a supported driver, prints a fatal error in teh case of double registration
func AddDriver(name string, driverCreate DriverCreateFunc) {
	if drivers[name] != nil {
		klog.Fatalf("Multiple cable engine drivers attempting to register with name %q", name)
	}
	drivers[name] = driverCreate
}

// Returns a new driver according the required Backend
func NewDriver(localSubnets []string, localEndpoint types.SubmarinerEndpoint) (Driver, error) {
	driverCreate, ok := drivers[localEndpoint.Spec.Backend]
	if !ok {
		return nil, fmt.Errorf("unsupported cable type %s", localEndpoint.Spec.Backend)
	}
	return driverCreate(localSubnets, localEndpoint)
}

// Sets the default cable driver name, if it is not specified by user.
func SetDefaultCableDriver(driver string) {
	defaultCableDriver = driver
}

// Returns the default cable driver name
func GetDefaultCableDriver() string {
	return defaultCableDriver
}
