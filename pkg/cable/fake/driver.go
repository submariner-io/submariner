/*
© 2021 Red Hat, Inc. and others

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
package fake

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	"github.com/submariner-io/submariner/pkg/types"
)

const DriverName = "fake-driver"

type Driver struct {
	sync.Mutex
	init                        chan struct{}
	ErrOnInit                   error
	ActiveConnections           map[string]interface{}
	Connections                 interface{}
	connectToEndpoint           chan *natdiscovery.NATEndpointInfo
	ErrOnConnectToEndpoint      error
	disconnectFromEndpoint      chan *types.SubmarinerEndpoint
	ErrOnDisconnectFromEndpoint error
}

func New() *Driver {
	return &Driver{
		init:                   make(chan struct{}),
		ActiveConnections:      map[string]interface{}{},
		connectToEndpoint:      make(chan *natdiscovery.NATEndpointInfo, 50),
		disconnectFromEndpoint: make(chan *types.SubmarinerEndpoint, 50),
	}
}

func (d *Driver) Init() error {
	defer GinkgoRecover()
	Expect(d.init).ToNot(BeClosed())
	close(d.init)

	return d.ErrOnInit
}

func (d *Driver) GetActiveConnections(clusterID string) ([]v1.Connection, error) {
	d.Lock()
	defer d.Unlock()

	value, ok := d.ActiveConnections[clusterID]
	if ok {
		if err, ok := value.(error); ok {
			return nil, err
		}

		return value.([]v1.Connection), nil
	}

	return nil, nil
}

func (d *Driver) GetConnections() ([]v1.Connection, error) {
	if d.Connections == nil {
		return []v1.Connection{}, nil
	}

	if err, ok := d.Connections.(error); ok {
		return nil, err
	}

	return d.Connections.([]v1.Connection), nil
}

func (d *Driver) ConnectToEndpoint(endpointInfo *natdiscovery.NATEndpointInfo) (string, error) {
	d.Lock()
	defer d.Unlock()

	err := d.ErrOnConnectToEndpoint
	if err != nil {
		d.ErrOnConnectToEndpoint = nil
		return "", err
	}

	d.ActiveConnections[endpointInfo.Endpoint.Spec.ClusterID] = []v1.Connection{
		{Endpoint: endpointInfo.Endpoint.Spec, UsingIP: endpointInfo.Endpoint.Spec.PublicIP, UsingNAT: true}}

	d.connectToEndpoint <- endpointInfo

	return endpointInfo.UseIP, nil
}

func (d *Driver) DisconnectFromEndpoint(endpoint types.SubmarinerEndpoint) error {
	d.Lock()
	defer d.Unlock()

	err := d.ErrOnDisconnectFromEndpoint
	if err != nil {
		d.ErrOnDisconnectFromEndpoint = nil
		return err
	}

	delete(d.ActiveConnections, endpoint.Spec.ClusterID)

	d.disconnectFromEndpoint <- &endpoint

	return nil
}

func (d *Driver) GetName() string {
	return DriverName
}

func (d *Driver) AwaitInit() {
	Eventually(d.init, 5).Should(BeClosed(), "Init was not called")
}

func (d *Driver) AwaitConnectToEndpoint(expected *natdiscovery.NATEndpointInfo) {
	Eventually(d.connectToEndpoint, 5).Should(Receive(Equal(expected)))
}

func (d *Driver) AwaitNoConnectToEndpoint() {
	Consistently(d.connectToEndpoint, 500*time.Millisecond).ShouldNot(Receive(), "ConnectToEndpoint was unexpectedly called")
}

func (d *Driver) AwaitDisconnectFromEndpoint(expected *v1.EndpointSpec) {
	Eventually(d.disconnectFromEndpoint, 5).Should(Receive(Equal(&types.SubmarinerEndpoint{Spec: *expected})))
}

func (d *Driver) AwaitNoDisconnectFromEndpoint() {
	Consistently(d.disconnectFromEndpoint, 500*time.Millisecond).ShouldNot(Receive(), "DisconnectFromEndpoint was unexpectedly called")
}
