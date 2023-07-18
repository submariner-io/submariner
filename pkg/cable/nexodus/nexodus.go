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

package nexodus

import (
	"github.com/submariner-io/admiral/pkg/log"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	"github.com/submariner-io/submariner/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var logger = log.Logger{Logger: logf.Log.WithName("nexodus")}

const (
	cableDriverName   = "nexodus"
	DefaultDeviceName = "wg0"
)

type nexodus struct {
	localEndpoint types.SubmarinerEndpoint
	localCluster  types.SubmarinerCluster
	connections   []v1.Connection
}

func init() {
	cable.AddDriver(cableDriverName, NewNexodus)
}

func NewNexodus(localEndpoint *types.SubmarinerEndpoint, localCluster *types.SubmarinerCluster) (cable.Driver, error) {
	v := nexodus{
		localEndpoint: *localEndpoint,
		localCluster:  *localCluster,
		connections:   []v1.Connection{},
	}

	return &v, nil
}

func (n *nexodus) ConnectToEndpoint(endpointInfo *natdiscovery.NATEndpointInfo) (string, error) {
	// We'll panic if endpointInfo is nil, this is intentional
	endpoint := &endpointInfo.Endpoint
	n.connections = append(n.connections,
		v1.Connection{Endpoint: endpoint.Spec, Status: v1.Connected, UsingIP: endpointInfo.UseIP, UsingNAT: endpointInfo.UseNAT})

	logger.V(log.DEBUG).Infof("Connect request made to remote endpoint %s", endpoint.Spec.ClusterID)

	return endpointInfo.UseIP, nil
}

func (n *nexodus) DisconnectFromEndpoint(remoteEndpoint *types.SubmarinerEndpoint) error {
	n.connections = removeConnectionForEndpoint(n.connections, remoteEndpoint)

	logger.V(log.DEBUG).Infof("Disconnect from remote endpoint %#v", remoteEndpoint.Spec.ClusterID)

	return nil
}

func removeConnectionForEndpoint(connections []v1.Connection, endpoint *types.SubmarinerEndpoint) []v1.Connection {
	for j := range connections {
		if connections[j].Endpoint.CableName == endpoint.Spec.CableName {
			copy(connections[j:], connections[j+1:])
			return connections[:len(connections)-1]
		}
	}

	return connections
}

func (n *nexodus) GetConnections() ([]v1.Connection, error) {
	return n.connections, nil
}

func (n *nexodus) GetActiveConnections() ([]v1.Connection, error) {
	return n.connections, nil
}

func (n *nexodus) Init() error {
	return nil
}

func (n *nexodus) GetName() string {
	return cableDriverName
}

func (n *nexodus) Cleanup() error {
	logger.Infof("Uninstalling the nexodus cable driver")
	return nil
}
