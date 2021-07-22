/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cable

import (
	"fmt"
	"strings"

	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/natdiscovery"

	"github.com/submariner-io/submariner/pkg/types"
	"k8s.io/klog"
)

// Driver is used by the ipsec engine to actually connect the tunnels.
type Driver interface {

	// Init initializes the driver with any state it needs.
	Init() error

	// GetActiveConnections returns an array of all the active connections.
	GetActiveConnections() ([]v1.Connection, error)

	// GetConnections() returns an array of the existing connections, including status and endpoint info
	GetConnections() ([]v1.Connection, error)

	// ConnectToEndpoint establishes a connection to the given endpoint and returns a string
	// representation of the IP address of the target endpoint.
	ConnectToEndpoint(endpointInfo *natdiscovery.NATEndpointInfo) (string, error)

	// DisconnectFromEndpoint disconnects from the connection to the given endpoint.
	DisconnectFromEndpoint(endpoint types.SubmarinerEndpoint) error

	// GetName returns driver's name
	GetName() string
}

// Function prototype to create a new driver
type DriverCreateFunc func(localEndpoint types.SubmarinerEndpoint, localCluster types.SubmarinerCluster) (Driver, error)

// Static map of supported drivers
var drivers = map[string]DriverCreateFunc{}

// Default name of the cable driver
var defaultCableDriver string

// Adds a supported driver, prints a fatal error in the case of double registration
func AddDriver(name string, driverCreate DriverCreateFunc) {
	if drivers[name] != nil {
		klog.Fatalf("Multiple cable engine drivers attempting to register with name %q", name)
	}

	drivers[name] = driverCreate
}

// Returns a new driver according the required Backend
func NewDriver(localEndpoint types.SubmarinerEndpoint, localCluster types.SubmarinerCluster) (Driver, error) {
	driverCreate, ok := drivers[localEndpoint.Spec.Backend]
	if !ok {
		var driverList strings.Builder

		for driver := range drivers {
			if driverList.Len() > 0 {
				driverList.WriteString(", ")
			}

			driverList.WriteString(driver)
		}

		return nil, fmt.Errorf("unsupported cable type %s; supported types: %s", localEndpoint.Spec.Backend, driverList.String())
	}

	return driverCreate(localEndpoint, localCluster)
}

// Sets the default cable driver name, if it is not specified by user.
func SetDefaultCableDriver(driver string) {
	defaultCableDriver = driver
}

// Returns the default cable driver name
func GetDefaultCableDriver() string {
	return defaultCableDriver
}
