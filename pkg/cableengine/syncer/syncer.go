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
	goerrors "errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/util"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cableengine"
	"github.com/submariner-io/submariner/pkg/cableengine/healthchecker"
	v1typed "github.com/submariner-io/submariner/pkg/client/clientset/versioned/typed/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/pinger"
	gwpod "github.com/submariner-io/submariner/pkg/pod"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	corev1typed "k8s.io/client-go/kubernetes/typed/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type GatewaySyncer struct {
	mutex       sync.Mutex
	gateways    v1typed.GatewayInterface
	pods        corev1typed.PodInterface
	engine      cableengine.Engine
	version     string
	statusError error
	healthCheck healthchecker.Interface
}

var (
	GatewayUpdateInterval = 5 * time.Second
	GatewayStaleTimeout   = GatewayUpdateInterval * 3
)

//nolint:promlinter // Existing public API, we can't change it to include "_total"
var gatewaySyncIterations = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "submariner_gateway_sync_iterations",
	Help: "Gateway synchronization iterations",
})

var logger = log.Logger{Logger: logf.Log.WithName("GWSyncer")}

const UpdateTimestampAnnotation = "update-timestamp"

func init() {
	prometheus.MustRegister(gatewaySyncIterations)
}

// NewGatewaySyncer creates a new Engine for the local cluster.
func NewGatewaySyncer(engine cableengine.Engine, gateways v1typed.GatewayInterface, pods corev1typed.PodInterface,
	version string, healthCheck healthchecker.Interface,
) *GatewaySyncer {
	return &GatewaySyncer{
		gateways:    gateways,
		pods:        pods,
		engine:      engine,
		version:     version,
		healthCheck: healthCheck,
	}
}

func (gs *GatewaySyncer) Run(stopCh <-chan struct{}) {
	wait.UntilWithContext(wait.ContextForChannel(stopCh), func(ctx context.Context) {
		name := gs.syncGatewayStatus(ctx)

		if gs.engine.GetHAStatus() == v1.HAStatusActive {
			err := gs.cleanupStaleGatewayEntries(ctx, name)
			if err != nil {
				utilruntime.HandleError(fmt.Errorf("error cleaning up stale gateway entries: %w", err))
			}

			err = gs.cleanupStaleGatewayPods(ctx)
			if err != nil {
				utilruntime.HandleError(fmt.Errorf("error cleaning up stale gateway pods: %w", err))
			}
		}
	}, GatewayUpdateInterval)

	gs.CleanupGatewayEntry(context.Background())

	logger.Info("CableEngine syncer stopped")
}

func (gs *GatewaySyncer) syncGatewayStatus(ctx context.Context) string {
	gs.mutex.Lock()
	defer gs.mutex.Unlock()

	return gs.syncGatewayStatusSafe(ctx)
}

func (gs *GatewaySyncer) SetGatewayStatusError(ctx context.Context, err error) {
	gs.mutex.Lock()
	defer gs.mutex.Unlock()

	gs.statusError = err
	gs.syncGatewayStatusSafe(ctx)
}

func (gs *GatewaySyncer) gatewayResourceInterface() resource.Interface[*v1.Gateway] {
	return &resource.InterfaceFuncs[*v1.Gateway]{
		GetFunc:    gs.gateways.Get,
		CreateFunc: gs.gateways.Create,
		UpdateFunc: gs.gateways.Update,
		DeleteFunc: gs.gateways.Delete,
	}
}

func (gs *GatewaySyncer) syncGatewayStatusSafe(ctx context.Context) string {
	logger.V(log.TRACE).Info("Running Gateway status sync")
	gatewaySyncIterations.Inc()

	gatewayObj := gs.generateGatewayObject()

	result, err := util.CreateOrUpdate(ctx, gs.gatewayResourceInterface(), gatewayObj,
		func(existing *v1.Gateway) (*v1.Gateway, error) {
			existing.Status = gatewayObj.Status

			if existing.Annotations == nil {
				existing.Annotations = map[string]string{}
			}

			existing.Annotations[UpdateTimestampAnnotation] = gatewayObj.Annotations[UpdateTimestampAnnotation]

			return existing, nil
		})
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("error creating/updating Gateway: %w", err))
		return gatewayObj.Name
	}

	if result == util.OperationResultCreated {
		logger.V(log.TRACE).Infof("Gateway does not exist - created: %+v", gatewayObj)
	} else if result == util.OperationResultUpdated {
		logger.V(log.TRACE).Infof("Gateway already exists - updated %+v", gatewayObj)
	} else {
		logger.V(log.TRACE).Info("Gateway already exists but doesn't need updating")
	}

	return gatewayObj.Name
}

func (gs *GatewaySyncer) cleanupStaleGatewayEntries(ctx context.Context, localGatewayName string) error {
	gateways, err := gs.gateways.List(ctx, metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "error listing Gateways")
	}

	for i := range gateways.Items {
		gw := &gateways.Items[i]
		if gw.Name == localGatewayName {
			continue
		}

		stale, err := isGatewayStale(gw)
		if err != nil {
			// In this case we don't want to stop the cleanup loop and just log it
			utilruntime.HandleError(fmt.Errorf("error processing stale Gateway %+v: %w", gw, err))
		}

		if stale {
			err := gs.gateways.Delete(ctx, gw.Name, metav1.DeleteOptions{})
			if err != nil {
				// In this case we don't want to stop the cleanup loop and just log it.
				utilruntime.HandleError(fmt.Errorf("error deleting stale Gateway %+v: %w", gw, err))
			} else {
				logger.Warningf("Deleted stale gateway: %s, didn't report for %s",
					gw.Name, GatewayStaleTimeout)
			}
		}
	}

	return nil
}

func isGatewayStale(gateway *v1.Gateway) (bool, error) {
	timestamp, ok := gateway.ObjectMeta.Annotations[UpdateTimestampAnnotation]
	if !ok {
		return true, fmt.Errorf("%q annotation not found", UpdateTimestampAnnotation)
	}

	timestampInt, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return true, fmt.Errorf("error parsing update-timestamp: %w", err)
	}

	now := time.Now().UTC().Unix()

	return now >= timestampInt+int64(GatewayStaleTimeout.Seconds()), nil
}

var gatewayStatusActiveSelector = labels.SelectorFromSet(map[string]string{gwpod.GatewayStatusLabel: string(v1.HAStatusActive)}).String()

func (gs *GatewaySyncer) cleanupStaleGatewayPods(ctx context.Context) error {
	localGatewayPodName := os.Getenv("POD_NAME")

	pods, err := gs.pods.List(ctx, metav1.ListOptions{
		LabelSelector: gatewayStatusActiveSelector,
	})
	if err != nil {
		return errors.Wrap(err, "error listing Gateway pods")
	}

	var errs []error

	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Name == localGatewayPodName {
			continue
		}

		logger.Infof("Found stale active gateway pod %q - setting to passive", pod.Name)

		err = util.Update(ctx, &resource.InterfaceFuncs[*corev1.Pod]{
			GetFunc:    gs.pods.Get,
			UpdateFunc: gs.pods.Update,
		}, pod, func(existing *corev1.Pod) (*corev1.Pod, error) {
			existing.Labels[gwpod.GatewayStatusLabel] = string(v1.HAStatusPassive)
			return existing, nil
		})
		errs = append(errs, err)
	}

	return goerrors.Join(errs...)
}

func (gs *GatewaySyncer) generateGatewayObject() *v1.Gateway {
	localEndpoint := gs.engine.GetLocalEndpoint()

	gateway := v1.Gateway{
		Status: v1.GatewayStatus{
			Version:       gs.version,
			LocalEndpoint: localEndpoint.Spec,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        resource.EnsureValidName(localEndpoint.Spec.Hostname),
			Annotations: map[string]string{UpdateTimestampAnnotation: strconv.FormatInt(time.Now().UTC().Unix(), 10)},
		},
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
			logger.Error(nil, msg)
			gateway.Status.StatusFailure = msg
		}
	}

	if connections == nil {
		connections = []v1.Connection{}
	}

	if gs.healthCheck != nil {
		for index := range connections {
			connection := &connections[index]

			latencyInfo := gs.healthCheck.GetLatencyInfo(&connection.Endpoint, connection.GetFamily())
			if latencyInfo != nil {
				connection.LatencyRTT = latencyInfo.Spec
				connection.Endpoint.SetHealthCheckIP(latencyInfo.IP)

				if connection.Status == v1.Connected {
					lastRTT, _ := time.ParseDuration(latencyInfo.Spec.Last)
					cable.RecordConnectionLatency(localEndpoint.Spec.Backend, &localEndpoint.Spec, &connection.Endpoint, lastRTT.Seconds(),
						connection.GetFamily())

					if connection.StatusMessage != "" {
						connection.StatusMessage = ""
					}

					if latencyInfo.ConnectionStatus == pinger.ConnectionError {
						connection.Status = v1.ConnectionError
						connection.StatusMessage = latencyInfo.ConnectionError
					} else if latencyInfo.ConnectionStatus == pinger.ConnectionUnknown {
						connection.StatusMessage = latencyInfo.ConnectionError
					}
				} else if connection.Status == v1.ConnectionError && latencyInfo.ConnectionStatus == pinger.Connected {
					connection.Status = v1.Connected
					connection.StatusMessage = ""
				}
			}
		}
	}

	gateway.Status.Connections = connections

	logger.V(log.TRACE).Infof("Generated Gateway object: %+v", gateway)

	return &gateway
}

// CleanupGatewayEntry removes this Gateway entry from the k8s API, it does not
// propagate error up because it's a termination function that we also provide externally.
func (gs *GatewaySyncer) CleanupGatewayEntry(ctx context.Context) {
	gatewayName := resource.EnsureValidName(gs.engine.GetLocalEndpoint().Spec.Hostname)

	err := gs.gateways.Delete(ctx, gatewayName, metav1.DeleteOptions{})
	if err != nil {
		logger.Errorf(err, "Error while trying to delete own Gateway %q", gatewayName)
		return
	}

	logger.Infof("The Gateway resource %q has been deleted", gatewayName)
}
