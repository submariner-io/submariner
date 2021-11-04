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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	submv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

const (
	cableDriverLabel       = "cable_driver"
	localClusterLabel      = "local_cluster"
	localHostnameLabel     = "local_hostname"
	localEndpointIPLabel   = "local_endpoint_ip"
	remoteClusterLabel     = "remote_cluster"
	remoteHostnameLabel    = "remote_hostname"
	remoteEndpointIPLabel  = "remote_endpoint_ip"
	connectionsStatusLabel = "status"
)

var (
	// The following metrics are gauges because we want to set the absolute value  RX/TX metrics.
	rxGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "submariner_gateway_rx_bytes",
			Help: "Count of bytes received (by cable driver and cable)",
		},
		[]string{
			cableDriverLabel,
			localClusterLabel,
			localHostnameLabel,
			localEndpointIPLabel,
			remoteClusterLabel,
			remoteHostnameLabel,
			remoteEndpointIPLabel,
		},
	)
	txGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "submariner_gateway_tx_bytes",
			Help: "Count of bytes transmitted (by cable driver and cable)",
		},
		[]string{
			cableDriverLabel,
			localClusterLabel,
			localHostnameLabel,
			localEndpointIPLabel,
			remoteClusterLabel,
			remoteHostnameLabel,
			remoteEndpointIPLabel,
		},
	)
	connectionsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "submariner_connections",
			Help: "Number of connections and corresponding status (by cable driver and cable)",
		},
		[]string{
			cableDriverLabel,
			localClusterLabel,
			localHostnameLabel,
			localEndpointIPLabel,
			remoteClusterLabel,
			remoteHostnameLabel,
			remoteEndpointIPLabel,
			connectionsStatusLabel,
		},
	)
	connectionEstablishedTimestampGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "submariner_connection_established_timestamp",
			Help: "Timestamp of last successful connection established (by cable driver and cable)",
		},
		[]string{
			cableDriverLabel,
			localClusterLabel,
			localHostnameLabel,
			localEndpointIPLabel,
			remoteClusterLabel,
			remoteHostnameLabel,
			remoteEndpointIPLabel,
		},
	)
	connectionLatencySecondsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "submariner_connection_latency_seconds",
			Help: "Connection latency in seconds (last RTT, by cable driver and cable)",
		},
		[]string{
			cableDriverLabel,
			localClusterLabel,
			localHostnameLabel,
			localEndpointIPLabel,
			remoteClusterLabel,
			remoteHostnameLabel,
			remoteEndpointIPLabel,
		},
	)
)

func init() {
	prometheus.MustRegister(rxGauge, txGauge, connectionsGauge, connectionEstablishedTimestampGauge, connectionLatencySecondsGauge)
}

func getLabels(cableDriverName string, localEndpoint, remoteEndpoint *submv1.EndpointSpec) prometheus.Labels {
	return prometheus.Labels{
		cableDriverLabel:      cableDriverName,
		localClusterLabel:     localEndpoint.ClusterID,
		localHostnameLabel:    localEndpoint.Hostname,
		localEndpointIPLabel:  localEndpoint.PublicIP,
		remoteClusterLabel:    remoteEndpoint.ClusterID,
		remoteHostnameLabel:   remoteEndpoint.Hostname,
		remoteEndpointIPLabel: remoteEndpoint.PublicIP,
	}
}

func RecordRxBytes(cableDriverName string, localEndpoint, remoteEndpoint *submv1.EndpointSpec, bytes int) {
	rxGauge.With(getLabels(cableDriverName, localEndpoint, remoteEndpoint)).Set(float64(bytes))
}

func RecordTxBytes(cableDriverName string, localEndpoint, remoteEndpoint *submv1.EndpointSpec, bytes int) {
	txGauge.With(getLabels(cableDriverName, localEndpoint, remoteEndpoint)).Set(float64(bytes))
}

func RecordConnectionLatency(cableDriverName string, localEndpoint, remoteEndpoint *submv1.EndpointSpec, latencySeconds float64) {
	connectionLatencySecondsGauge.With(getLabels(cableDriverName, localEndpoint, remoteEndpoint)).Set(latencySeconds)
}

func RecordConnection(cableDriverName string, localEndpoint, remoteEndpoint *submv1.EndpointSpec, status string, isNew bool) {
	labels := getLabels(cableDriverName, localEndpoint, remoteEndpoint)

	if isNew {
		connectionEstablishedTimestampGauge.With(labels).Set(float64(time.Now().Unix()))
	}

	labels[connectionsStatusLabel] = status
	connectionsGauge.With(labels).Set(1)
}

func RecordDisconnected(cableDriverName string, localEndpoint, remoteEndpoint *submv1.EndpointSpec) {
	labels := getLabels(cableDriverName, localEndpoint, remoteEndpoint)

	connectionLatencySecondsGauge.Delete(labels)
	connectionEstablishedTimestampGauge.Delete(labels)
	rxGauge.Delete(labels)
	txGauge.Delete(labels)
	connectionsGauge.Delete(labels)
}

func RecordNoConnections() {
	// TODO: assuming only 1 cable driver is active at a time, calling Reset() will work.
	// once this is changed, there is a need to be updated accordingly
	connectionsGauge.Reset()
}
