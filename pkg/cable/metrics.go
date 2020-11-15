/*
© 2020 Red Hat, Inc. and others.

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
	cableDriverLabel      = "cable_driver"
	localClusterLabel     = "local_cluster"
	localHostnameLabel    = "local_hostname"
	localEndpointIpLabel  = "local_endpoint_ip"
	remoteClusterLabel    = "remote_cluster"
	remoteHostnameLabel   = "remote_hostname"
	remoteEndpointIpLabel = "remote_endpoint_ip"
)

var (
	// The following metrics are gauges because we want to set the absolute value
	// RX/TX metrics
	rxGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gateway_rx_bytes",
			Help: "Count of bytes received (by cable driver and cable)",
		},
		[]string{
			cableDriverLabel,
			localClusterLabel,
			localHostnameLabel,
			localEndpointIpLabel,
			remoteClusterLabel,
			remoteHostnameLabel,
			remoteEndpointIpLabel,
		},
	)
	txGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gateway_tx_bytes",
			Help: "Count of bytes transmitted (by cable driver and cable)",
		},
		[]string{
			cableDriverLabel,
			localClusterLabel,
			localHostnameLabel,
			localEndpointIpLabel,
			remoteClusterLabel,
			remoteHostnameLabel,
			remoteEndpointIpLabel,
		},
	)
	connectionStatusGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "connection_status",
			Help: "Number of connections and corresponding status (by cable driver and cable)",
		},
		[]string{
			cableDriverLabel,
			localClusterLabel,
			localHostnameLabel,
			localEndpointIpLabel,
			remoteClusterLabel,
			remoteHostnameLabel,
			remoteEndpointIpLabel,
		},
	)
	connectionEstablishedTimestampGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "connection_established_timestamp",
			Help: "Timestamp of last successful connection established (by cable driver and cable)",
		},
		[]string{
			cableDriverLabel,
			localClusterLabel,
			localHostnameLabel,
			localEndpointIpLabel,
			remoteClusterLabel,
			remoteHostnameLabel,
			remoteEndpointIpLabel,
		},
	)
	connectionLatencySecondsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "connection_latency_seconds",
			Help: "Connection latency in seconds (average RTT, by cable driver and cable)",
		},
		[]string{
			cableDriverLabel,
			localClusterLabel,
			localHostnameLabel,
			localEndpointIpLabel,
			remoteClusterLabel,
			remoteHostnameLabel,
			remoteEndpointIpLabel,
		},
	)
)

func init() {
	prometheus.MustRegister(rxGauge, txGauge, connectionStatusGauge, connectionEstablishedTimestampGauge, connectionLatencySecondsGauge)
}

func getLabels(cableDriverName string, localEndpoint, remoteEndpoint *submv1.EndpointSpec) prometheus.Labels {
	return prometheus.Labels{
		cableDriverLabel:      cableDriverName,
		localClusterLabel:     localEndpoint.ClusterID,
		localHostnameLabel:    localEndpoint.Hostname,
		localEndpointIpLabel:  localEndpoint.PublicIP,
		remoteClusterLabel:    remoteEndpoint.ClusterID,
		remoteHostnameLabel:   remoteEndpoint.Hostname,
		remoteEndpointIpLabel: remoteEndpoint.PublicIP,
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

func RecordConnectionStatusActive(cableDriverName string, localEndpoint, remoteEndpoint *submv1.EndpointSpec) {
	labels := getLabels(cableDriverName, localEndpoint, remoteEndpoint)

	connectionStatusGauge.With(labels).Set(float64(1))
	connectionEstablishedTimestampGauge.With(labels).Set(float64(time.Now().Unix()))
}

func RecordConnectionStatusInactive(cableDriverName string, localEndpoint, remoteEndpoint *submv1.EndpointSpec) {
	labels := getLabels(cableDriverName, localEndpoint, remoteEndpoint)

	txGauge.Delete(labels)
	rxGauge.Delete(labels)
	connectionStatusGauge.Delete(labels)
	connectionEstablishedTimestampGauge.Delete(labels)
	connectionLatencySecondsGauge.Delete(labels)
}
