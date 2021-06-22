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
package syncer

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/submariner-io/admiral/pkg/log"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cableengine"
	"github.com/submariner-io/submariner/pkg/cableengine/healthchecker"
	v1typed "github.com/submariner-io/submariner/pkg/client/clientset/versioned/typed/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"
)

type GatewaySyncer struct {
	sync.Mutex
	client      v1typed.GatewayInterface
	engine      cableengine.Engine
	version     string
	statusError error
	healthCheck healthchecker.Interface
}

var GatewayUpdateInterval = 5 * time.Second
var GatewayStaleTimeout = GatewayUpdateInterval * 3

var gatewaySyncIterations = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "submariner_gateway_sync_iterations",
	Help: "Gateway synchronization iterations",
})

const updateTimestampAnnotation = "update-timestamp"

func init() {
	prometheus.MustRegister(gatewaySyncIterations)
}

// NewEngine creates a new Engine for the local cluster
func NewGatewaySyncer(engine cableengine.Engine, client v1typed.GatewayInterface,
	version string, healthCheck healthchecker.Interface) *GatewaySyncer {
	return &GatewaySyncer{
		client:      client,
		engine:      engine,
		version:     version,
		healthCheck: healthCheck,
	}
}

func (gs *GatewaySyncer) Run(stopCh <-chan struct{}) {
	go func() {
		wait.Until(gs.syncGatewayStatus, GatewayUpdateInterval, stopCh)
		gs.CleanupGatewayEntry()
	}()

	klog.Info("CableEngine syncer started")
}

func (gs *GatewaySyncer) syncGatewayStatus() {
	gs.Lock()
	defer gs.Unlock()

	gs.syncGatewayStatusSafe()
}

func (gs *GatewaySyncer) SetGatewayStatusError(err error) {
	gs.Lock()
	defer gs.Unlock()

	gs.statusError = err
	gs.syncGatewayStatusSafe()
}

func (gs *GatewaySyncer) syncGatewayStatusSafe() {
	klog.V(log.TRACE).Info("Running Gateway status sync")
	gatewaySyncIterations.Inc()

	gatewayObj := gs.generateGatewayObject()

	existingGw, err := gs.getLastSyncedGateway(gatewayObj.Name)

	if errors.IsNotFound(err) {
		klog.V(log.TRACE).Infof("Gateway does not exist - creating: %+v", gatewayObj)
		_, err = gs.client.Create(context.TODO(), gatewayObj, metav1.CreateOptions{})
		if err != nil {
			utilruntime.HandleError(fmt.Errorf("error creating Gateway object %+v: %s", gatewayObj, err))
			return
		}
	} else if err != nil {
		utilruntime.HandleError(fmt.Errorf("error getting existing Gateway: %s", err))
		return
	} else if !reflect.DeepEqual(gatewayObj.Status, existingGw.Status) {
		klog.V(log.TRACE).Infof("Gateway already exists - updating %+v", gatewayObj)
		existingGw.Status = gatewayObj.Status
		existingGw.Annotations = gatewayObj.Annotations

		_, err := gs.client.Update(context.TODO(), existingGw, metav1.UpdateOptions{})
		if err != nil {
			utilruntime.HandleError(fmt.Errorf("error updating Gateway object %+v: %s", gatewayObj, err))
			return
		}
	} else {
		klog.V(log.TRACE).Info("Gateway already exists but doesn't need updating")
	}

	if gatewayObj.Status.HAStatus == v1.HAStatusActive {
		err := gs.cleanupStaleGatewayEntries(gatewayObj.Name)
		if err != nil {
			utilruntime.HandleError(fmt.Errorf("error cleaning up stale gateway entries: %s", err))
		}
	}
}

func (gs *GatewaySyncer) cleanupStaleGatewayEntries(localGatewayName string) error {
	gateways, err := gs.client.List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}

	for _, gw := range gateways.Items {
		if gw.Name == localGatewayName {
			continue
		}

		stale, err := isGatewayStale(gw)
		if err != nil {
			// In this case we don't want to stop the cleanup loop and just log it
			utilruntime.HandleError(fmt.Errorf("error processing stale Gateway %+v: %s", gw, err))
		}

		if stale {
			err := gs.client.Delete(context.TODO(), gw.Name, metav1.DeleteOptions{})
			if err != nil {
				// In this case we don't want to stop the cleanup loop and just log it
				utilruntime.HandleError(fmt.Errorf("error deleting stale Gateway %+v: %s", gw, err))
			} else {
				klog.Warningf("Deleted stale gateway: %s, didn't report for %s",
					gw.Name, GatewayStaleTimeout)
			}
		}
	}

	return nil
}

func isGatewayStale(gateway v1.Gateway) (bool, error) {
	timestamp, ok := gateway.ObjectMeta.Annotations[updateTimestampAnnotation]
	if !ok {
		return true, fmt.Errorf("%q annotation not found", updateTimestampAnnotation)
	}

	timestampInt, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return true, fmt.Errorf("error parsing update-timestamp: %s", err)
	}

	now := time.Now().UTC().Unix()

	return now >= (timestampInt + int64(GatewayStaleTimeout.Seconds())), nil
}

func (gs *GatewaySyncer) getLastSyncedGateway(name string) (*v1.Gateway, error) {
	existingGw, err := gs.client.Get(context.TODO(), name, metav1.GetOptions{})
	klog.V(log.TRACE).Infof("Last synced Gateway: %+v", existingGw)

	return existingGw, err
}

func (gs *GatewaySyncer) generateGatewayObject() *v1.Gateway {
	localEndpoint := gs.engine.GetLocalEndpoint()

	gateway := v1.Gateway{
		Status: v1.GatewayStatus{
			Version:       gs.version,
			LocalEndpoint: localEndpoint.Spec,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        util.EnsureValidName(localEndpoint.Spec.Hostname),
			Annotations: map[string]string{updateTimestampAnnotation: strconv.FormatInt(time.Now().UTC().Unix(), 10)}},
	}

	gateway.Status.HAStatus = gs.engine.GetHAStatus()

	var connections []v1.Connection

	if gs.statusError != nil {
		gateway.Status.StatusFailure = gs.statusError.Error()
	} else {
		var err error
		connections, err = gs.engine.ListCableConnections()
		if err != nil {
			msg := fmt.Sprintf("Error retrieving driver connections: %s", err)
			klog.Errorf(msg)
			gateway.Status.StatusFailure = msg
		}
	}

	if connections == nil {
		connections = []v1.Connection{}
	}

	if gs.healthCheck != nil {
		for index := range connections {
			connection := &(connections)[index]
			latencyInfo := gs.healthCheck.GetLatencyInfo(&connection.Endpoint)
			if latencyInfo != nil {
				connection.LatencyRTT = latencyInfo.Spec
				if connection.Status == v1.Connected {
					lastRTT, _ := time.ParseDuration(latencyInfo.Spec.Last)
					cable.RecordConnectionLatency(localEndpoint.Spec.Backend, &localEndpoint.Spec, &connection.Endpoint, lastRTT.Seconds())

					if connection.StatusMessage != "" {
						connection.StatusMessage = ""
					}

					if latencyInfo.ConnectionStatus == healthchecker.ConnectionError {
						connection.Status = v1.ConnectionError
						connection.StatusMessage = latencyInfo.ConnectionError
					} else if latencyInfo.ConnectionStatus == healthchecker.ConnectionUnknown {
						connection.StatusMessage = latencyInfo.ConnectionError
					}
				} else if connection.Status == v1.ConnectionError && latencyInfo.ConnectionStatus == healthchecker.Connected {
					connection.Status = v1.Connected
					connection.StatusMessage = ""
				}
			}
		}
	}

	gateway.Status.Connections = connections

	klog.V(log.TRACE).Infof("Generated Gateway object: %+v", gateway)

	return &gateway
}

// CleanupGatewayEntry removes this Gateway entry from the k8s API, it does not
// propagate error up because it's a termination function that we also provide externally
func (gs *GatewaySyncer) CleanupGatewayEntry() {
	hostName := gs.engine.GetLocalEndpoint().Spec.Hostname
	err := gs.client.Delete(context.TODO(), hostName, metav1.DeleteOptions{})
	if err != nil {
		klog.Errorf("Error while trying to delete own Gateway %q : %s", hostName, err)
		return
	}

	klog.Infof("The Gateway entry for %q has been deleted", hostName)
}
