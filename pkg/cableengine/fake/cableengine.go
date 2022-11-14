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

package fake

import (
	"sync"

	. "github.com/onsi/gomega"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cableengine"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	"github.com/submariner-io/submariner/pkg/types"
)

type Engine struct { //nolint:gocritic // This mutex is exposed but we tweak it in tests
	sync.Mutex
	Connections               []v1.Connection
	ListCableConnectionsError error
	HAStatus                  v1.HAStatus
	LocalEndPoint             *types.SubmarinerEndpoint
	installCable              chan *v1.EndpointSpec
	ErrOnInstallCable         error
	removeCable               chan *v1.EndpointSpec
	ErrOnRemoveCable          error
}

var _ cableengine.Engine = &Engine{}

func New() *Engine {
	return &Engine{
		HAStatus:     v1.HAStatusPassive,
		Connections:  []v1.Connection{},
		installCable: make(chan *v1.EndpointSpec, 100),
		removeCable:  make(chan *v1.EndpointSpec, 100),
	}
}

func (e *Engine) StartEngine() error {
	return nil
}

func (e *Engine) InstallCable(endpoint *v1.Endpoint) error {
	err := e.ErrOnInstallCable
	if err != nil {
		e.ErrOnInstallCable = nil
		return err
	}

	e.installCable <- &endpoint.Spec

	return nil
}

func (e *Engine) RemoveCable(endpoint *v1.Endpoint) error {
	err := e.ErrOnRemoveCable
	if err != nil {
		e.ErrOnRemoveCable = nil
		return err
	}

	e.removeCable <- &endpoint.Spec

	return nil
}

func (e *Engine) GetLocalEndpoint() *types.SubmarinerEndpoint {
	return e.LocalEndPoint
}

func (e *Engine) ListCableConnections() ([]v1.Connection, error) {
	e.Lock()
	defer e.Unlock()

	if e.ListCableConnectionsError != nil {
		return nil, e.ListCableConnectionsError
	}

	return e.Connections, nil
}

func (e *Engine) GetHAStatus() v1.HAStatus {
	e.Lock()
	ret := e.HAStatus
	e.Unlock()

	return ret
}

func (e *Engine) VerifyInstallCable(expected *v1.EndpointSpec) {
	Eventually(e.installCable, 5).Should(Receive(Equal(expected)), "InstallCable was not invoked")
}

func (e *Engine) VerifyRemoveCable(expected *v1.EndpointSpec) {
	Eventually(e.removeCable, 5).Should(Receive(Equal(expected)), "RemoveCable was not invoked")
}

func (e *Engine) SetupNATDiscovery(natDiscovery natdiscovery.Interface) {
}

func (e *Engine) Cleanup() error {
	return nil
}
