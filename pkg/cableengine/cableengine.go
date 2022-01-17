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

package cableengine

//nolint:gci // The supported driver imports are kept separate.
import (
	"reflect"
	"sync"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	"github.com/submariner-io/submariner/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"

	// Add supported drivers.
	_ "github.com/submariner-io/submariner/pkg/cable/libreswan"
	_ "github.com/submariner-io/submariner/pkg/cable/vxlan"
	_ "github.com/submariner-io/submariner/pkg/cable/wireguard"
)

// Engine represents an implementation of some remote connectivity mechanism, such as
// a VPN gateway.
// An Engine cooperates with, and delegates work to, a cable.Driver for implementing
// a secure connection to remote clusters.
type Engine interface {
	// StartEngine performs any general set up work needed independent of any remote connections.
	StartEngine() error
	// InstallCable performs any set up work needed for connecting to given remote endpoint.
	// Once InstallCable completes, it should be possible to connect to remote
	// Pods or Services behind the given endpoint.
	InstallCable(remote *v1.Endpoint) error
	// RemoveCable disconnects the Engine from the given remote endpoint. Upon completion.
	// remote Pods and Service may not be accessible anymore.
	RemoveCable(remote *v1.Endpoint) error
	// ListCableConnections returns a list of cable connection, and the related status.
	ListCableConnections() ([]v1.Connection, error)
	// GetLocalEndpoint returns the local endpoint for this cable engine.
	GetLocalEndpoint() *types.SubmarinerEndpoint
	// GetHAStatus returns the HA status for this cable engine.
	GetHAStatus() v1.HAStatus
	// SetupNATDiscovery configures the handler for nat discovery of the endpoints.
	SetupNATDiscovery(natDiscovery natdiscovery.Interface)
}

type engine struct {
	sync.Mutex
	driver              cable.Driver
	localCluster        types.SubmarinerCluster
	localEndpoint       types.SubmarinerEndpoint
	natDiscovery        natdiscovery.Interface
	natEndpointInfoCh   chan *natdiscovery.NATEndpointInfo
	natDiscoveryPending map[string]int
	installedCables     map[string]metav1.Time
}

// NewEngine creates a new Engine for the local cluster.
func NewEngine(localCluster *types.SubmarinerCluster, localEndpoint *types.SubmarinerEndpoint) Engine {
	// We'll panic if localCluster or localEndpoint are nil, this is intentional
	return &engine{
		localCluster:        *localCluster,
		localEndpoint:       *localEndpoint,
		natDiscoveryPending: map[string]int{},
		installedCables:     map[string]metav1.Time{},
	}
}

func (i *engine) GetLocalEndpoint() *types.SubmarinerEndpoint {
	return &i.localEndpoint
}

func (i *engine) StartEngine() error {
	i.Lock()
	defer i.Unlock()

	if err := i.startDriver(); err != nil {
		return err
	}

	klog.Infof("CableEngine controller started, driver: %q", i.driver.GetName())

	return nil
}

func (i *engine) startDriver() error {
	var err error

	if i.driver, err = cable.NewDriver(&i.localEndpoint, &i.localCluster); err != nil {
		return errors.Wrap(err, "error creating the cable driver")
	}

	return errors.Wrap(i.driver.Init(), "error initializing the cable driver")
}

func (i *engine) SetupNATDiscovery(natDiscovery natdiscovery.Interface) {
	i.natDiscovery = natDiscovery
	i.natEndpointInfoCh = natDiscovery.GetReadyChannel()

	go func() {
		for natEndpointInfo := range i.natEndpointInfoCh {
			err := i.installCableWithNATInfo(natEndpointInfo)
			if err != nil {
				klog.Errorf("Error installing cable for %#v: %v", natEndpointInfo, err)
			}
		}
	}()
}

func (i *engine) installCableWithNATInfo(rnat *natdiscovery.NATEndpointInfo) error {
	endpoint := &rnat.Endpoint

	i.Lock()
	defer i.Unlock()

	if _, ok := i.natDiscoveryPending[rnat.Endpoint.Spec.CableName]; !ok {
		return nil
	}

	i.natDiscoveryPending[rnat.Endpoint.Spec.CableName]--
	if i.natDiscoveryPending[rnat.Endpoint.Spec.CableName] == 0 {
		delete(i.natDiscoveryPending, rnat.Endpoint.Spec.CableName)
	}

	activeConnections, err := i.driver.GetActiveConnections()
	if err != nil {
		return errors.Wrap(err, "error getting the active connections")
	}

	for j := range activeConnections {
		active := &activeConnections[j]
		klog.V(log.TRACE).Infof("Analyzing currently active connection %q", active.Endpoint.CableName)

		if active.Endpoint.ClusterID != endpoint.Spec.ClusterID {
			continue
		}

		prevTimestamp := i.installedCables[active.Endpoint.CableName]

		klog.V(log.TRACE).Infof("Found a pre-existing cable %q with timestamp %q that belongs to this cluster %s",
			active.Endpoint.CableName, prevTimestamp, endpoint.Spec.ClusterID)

		if endpoint.CreationTimestamp.Before(&prevTimestamp) {
			klog.Warningf("The timestamp (%s) for new cable %q is older than the timestamp (%s) of the pre-existing "+
				"cable %q - not replacing", endpoint.CreationTimestamp, endpoint.Spec.CableName, prevTimestamp, active.Endpoint.CableName)
			return nil
		}

		if endpoint.CreationTimestamp.Equal(&prevTimestamp) && active.Endpoint.CableName == endpoint.Spec.CableName {
			// There could be scenarios where the cableName would be the same but the endpoint IP or specific driver
			// config has changed.
			if active.UsingIP == rnat.UseIP && active.UsingNAT == rnat.UseNAT &&
				reflect.DeepEqual(active.Endpoint.BackendConfig, endpoint.Spec.BackendConfig) {
				klog.V(log.TRACE).Infof("Connection info (IP: %s, NAT: %v, BackendConfig: %v) for cable %q is unchanged"+
					" - not re-installing", active.UsingIP, active.UsingNAT, active.Endpoint.BackendConfig, active.Endpoint.CableName)
				return nil
			}

			klog.V(log.DEBUG).Infof("New connection info (IP: %s, NAT: %v, BackendConfig: %v) for cable %q differs from"+
				" previous (IP: %s, NAT: %v, BackendConfig: %v) - re-installing", rnat.UseIP, rnat.UseNAT, active.Endpoint.BackendConfig,
				active.Endpoint.CableName, active.UsingIP, active.UsingNAT, endpoint.Spec.BackendConfig)
		}

		klog.V(log.DEBUG).Infof("Disconnecting pre-existing cable %q", active.Endpoint.CableName)

		err = i.driver.DisconnectFromEndpoint(&types.SubmarinerEndpoint{Spec: active.Endpoint})
		if err != nil {
			return errors.Wrapf(err, "error disconnecting previous Endpoint cable %#v", active.Endpoint)
		}
	}

	klog.Infof("Installing Endpoint cable %q", endpoint.Spec.CableName)

	remoteEndpointIP, err := i.driver.ConnectToEndpoint(rnat)
	if err != nil {
		return errors.Wrapf(err, "error installing Endpoint cable %q", endpoint.Spec.CableName)
	}

	klog.Infof("Successfully installed Endpoint cable %q with remote IP %s", endpoint.Spec.CableName, remoteEndpointIP)

	i.installedCables[rnat.Endpoint.Spec.CableName] = endpoint.CreationTimestamp

	return nil
}

func (i *engine) InstallCable(endpoint *v1.Endpoint) error {
	if endpoint.Spec.ClusterID == i.localCluster.ID {
		klog.V(log.TRACE).Infof("Not installing cable for local cluster")
		return nil
	}

	if reflect.DeepEqual(endpoint.Spec, i.localEndpoint.Spec) {
		klog.V(log.DEBUG).Infof("Not installing cable for local endpoint")
		return nil
	}

	i.Lock()
	i.natDiscoveryPending[endpoint.Spec.CableName]++
	i.Unlock()

	i.natDiscovery.AddEndpoint(endpoint)

	return nil
}

func (i *engine) RemoveCable(endpoint *v1.Endpoint) error {
	if endpoint.Spec.ClusterID == i.localCluster.ID {
		klog.V(log.DEBUG).Infof("Cables are not added/removed for the local cluster, skipping removal")
		return nil
	}

	klog.Infof("Removing Endpoint cable %q", endpoint.Spec.CableName)

	i.natDiscovery.RemoveEndpoint(endpoint.Spec.CableName)

	i.Lock()
	defer i.Unlock()

	delete(i.natDiscoveryPending, endpoint.Spec.CableName)

	if _, ok := i.installedCables[endpoint.Spec.CableName]; !ok {
		return nil
	}

	err := i.driver.DisconnectFromEndpoint(&types.SubmarinerEndpoint{Spec: endpoint.Spec})
	if err != nil {
		return errors.Wrapf(err, "error disconnecting Endpoint cable %q", endpoint.Spec.CableName)
	}

	delete(i.installedCables, endpoint.Spec.CableName)

	klog.Infof("Successfully removed Endpoint cable %q", endpoint.Spec.CableName)

	return nil
}

func (i *engine) GetHAStatus() v1.HAStatus {
	i.Lock()
	defer i.Unlock()

	if i.driver == nil {
		return v1.HAStatusPassive
	}

	// we may want to add a call to the driver in the future, for situations where
	// the driver is running from the start, but could be in passive status, or
	// in active/active.
	return v1.HAStatusActive
}

func (i *engine) ListCableConnections() ([]v1.Connection, error) {
	i.Lock()
	defer i.Unlock()

	if i.driver != nil {
		return i.driver.GetConnections() // nolint:wrapcheck  // Let the caller wrap it
	}
	// if no driver, we can safely report that no connections exist.
	return []v1.Connection{}, nil
}
