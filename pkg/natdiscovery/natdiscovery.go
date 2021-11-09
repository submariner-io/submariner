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

package natdiscovery

import (
	"crypto/rand"
	"math/big"
	"net"
	"reflect"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/util"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"
)

type Interface interface {
	Run(stopCh <-chan struct{}) error
	AddEndpoint(endpoint *v1.Endpoint)
	RemoveEndpoint(endpointName string)
	GetReadyChannel() chan *NATEndpointInfo
}

type (
	udpWriteFunction  func(b []byte, addr *net.UDPAddr) (int, error)
	findSrcIPFunction func(destinationIP string) string
)

type natDiscovery struct {
	sync.Mutex
	localEndpoint   *types.SubmarinerEndpoint
	remoteEndpoints map[string]*remoteEndpointNAT
	requestCounter  uint64
	serverUDPWrite  udpWriteFunction
	findSrcIP       findSrcIPFunction
	serverPort      int32
	readyChannel    chan *NATEndpointInfo
}

func New(localEndpoint *types.SubmarinerEndpoint) (Interface, error) {
	return newNATDiscovery(localEndpoint)
}

func newNATDiscovery(localEndpoint *types.SubmarinerEndpoint) (*natDiscovery, error) {
	requestCounter, err := randomRequestCounter()
	if err != nil {
		return nil, err
	}

	ndPort, err := localEndpoint.Spec.GetBackendPort(v1.NATTDiscoveryPortConfig, 0)
	if err != nil {
		return nil, errors.Wrap(err, "error parsing nat discovery port")
	}

	return &natDiscovery{
		localEndpoint:   localEndpoint,
		serverPort:      ndPort,
		remoteEndpoints: map[string]*remoteEndpointNAT{},
		findSrcIP:       util.GetLocalIPForDestination,
		requestCounter:  requestCounter,
		readyChannel:    make(chan *NATEndpointInfo, 100),
	}, nil
}

func randomRequestCounter() (uint64, error) {
	max := new(big.Int)
	max.Exp(big.NewInt(2), big.NewInt(64), nil).Sub(max, big.NewInt(1))

	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return 0, errors.Wrapf(err, "generating random request counter")
	}

	return n.Uint64(), nil
}

var errorNoNATDiscoveryPort = errors.New("NATT discovery port missing in endpoint")

func extractNATDiscoveryPort(endpoint *v1.EndpointSpec) (int32, error) {
	natDiscoveryPort, err := endpoint.GetBackendPort(v1.NATTDiscoveryPortConfig, 0)
	if err != nil {
		return natDiscoveryPort, err // nolint:wrapcheck  // No need to wrap this error
	}

	if natDiscoveryPort == 0 {
		return natDiscoveryPort, errorNoNATDiscoveryPort
	}

	return natDiscoveryPort, nil
}

func (nd *natDiscovery) GetReadyChannel() chan *NATEndpointInfo {
	return nd.readyChannel
}

func (nd *natDiscovery) Run(stopCh <-chan struct{}) error {
	klog.V(log.DEBUG).Infof("NAT discovery server starting on port %d", nd.serverPort)

	if err := nd.runListener(stopCh); err != nil {
		return err
	}

	go wait.Until(func() {
		klog.V(log.TRACE).Info("NAT discovery checking endpoint list")
		nd.checkEndpointList()
	}, time.Second, stopCh)

	return nil
}

func (nd *natDiscovery) AddEndpoint(endpoint *v1.Endpoint) {
	nd.Lock()
	defer nd.Unlock()

	if ep, exists := nd.remoteEndpoints[endpoint.Spec.CableName]; exists {
		if reflect.DeepEqual(ep.endpoint.Spec, endpoint.Spec) {
			if ep.isDiscoveryComplete() {
				nd.readyChannel <- ep.toNATEndpointInfo()
			}

			return
		}

		klog.V(log.DEBUG).Infof("NAT discovery updated endpoint %q", endpoint.Spec.CableName)
		delete(nd.remoteEndpoints, endpoint.Spec.CableName)
	}

	remoteNAT := newRemoteEndpointNAT(endpoint)

	// support nat discovery disabled or a remote cluster endpoint which still hasn't implemented this protocol
	if _, err := extractNATDiscoveryPort(&endpoint.Spec); err != nil || nd.serverPort == 0 {
		if !errors.Is(err, errorNoNATDiscoveryPort) {
			klog.Errorf("Error extracting NATT discovery port from endpoint %q: %v", endpoint.Spec.CableName, err)
		}

		remoteNAT.useLegacyNATSettings()
		nd.readyChannel <- remoteNAT.toNATEndpointInfo()
	} else {
		klog.Infof("Starting NAT discovery for endpoint %q", endpoint.Spec.CableName)
	}

	nd.remoteEndpoints[endpoint.Spec.CableName] = remoteNAT
}

func (nd *natDiscovery) RemoveEndpoint(endpointName string) {
	nd.Lock()
	defer nd.Unlock()
	delete(nd.remoteEndpoints, endpointName)
}

func (nd *natDiscovery) checkEndpointList() {
	nd.Lock()
	defer nd.Unlock()

	for _, endpointNAT := range nd.remoteEndpoints {
		name := endpointNAT.endpoint.Spec.CableName
		klog.V(log.TRACE).Infof("NAT processing remote endpoint %q", name)

		if endpointNAT.shouldCheck() {
			if endpointNAT.hasTimedOut() {
				klog.Warningf("NAT discovery for endpoint %q has timed out", name)
				endpointNAT.useLegacyNATSettings()
				nd.readyChannel <- endpointNAT.toNATEndpointInfo()
			} else if err := nd.sendCheckRequest(endpointNAT); err != nil {
				klog.Errorf("Error sending check request to endpoint %q: %s", name, err)
			}
		} else {
			klog.V(log.TRACE).Infof("NAT shouldCheck() == false for  %q", name)
		}
	}
}
