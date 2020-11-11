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
	"github.com/prometheus/client_golang/prometheus"

	submv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

const (
	cableDriverLabel    = "cable_driver"
	localClusterLabel   = "local_cluster"
	localHostnameLabel  = "local_hostname"
	remoteClusterLabel  = "remote_cluster"
	remoteHostnameLabel = "remote_hostname"
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
			remoteClusterLabel,
			remoteHostnameLabel,
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
			remoteClusterLabel,
			remoteHostnameLabel,
		},
	)
)

func init() {
	prometheus.MustRegister(rxGauge)
}

func RecordRxBytes(cableDriverName string, localEndpoint, remoteEndpoint *submv1.EndpointSpec, bytes int) {
	rxGauge.With(prometheus.Labels{
		cableDriverLabel:    cableDriverName,
		localClusterLabel:   localEndpoint.ClusterID,
		localHostnameLabel:  localEndpoint.Hostname,
		remoteClusterLabel:  remoteEndpoint.ClusterID,
		remoteHostnameLabel: remoteEndpoint.Hostname,
	}).Set(float64(bytes))
}

func RecordTxBytes(cableDriverName string, localEndpoint, remoteEndpoint *submv1.EndpointSpec, bytes int) {
	txGauge.With(prometheus.Labels{
		cableDriverLabel:    cableDriverName,
		localClusterLabel:   localEndpoint.ClusterID,
		localHostnameLabel:  localEndpoint.Hostname,
		remoteClusterLabel:  remoteEndpoint.ClusterID,
		remoteHostnameLabel: remoteEndpoint.Hostname,
	}).Set(float64(bytes))
}
