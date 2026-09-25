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
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	submv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	k8snet "k8s.io/utils/net"
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
	shortConnectionsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "submariner_connections_short",
			Help: "Summary of connections and corresponding status without cable information",
		},
		[]string{
			cableDriverLabel,
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
	prometheus.MustRegister(rxGauge, txGauge, connectionsGauge, shortConnectionsGauge, connectionEstablishedTimestampGauge,
		connectionLatencySecondsGauge)
}

type cableStatus struct {
	labels    prometheus.Labels
	status    string
	cableName string
}

type shortConnectionKey struct {
	cableDriverName string
	status          string
}

var (
	cableStatusMutex sync.Mutex
	// cableStatuses is keyed by remote cluster rather than by the full set of labels, as the cable
	// drivers key their own connections, so that a cable replacing an earlier one to the same cluster
	// takes its place instead of leaving it behind.
	cableStatuses = map[string]cableStatus{}
	// publishedShortConnections is what shortConnectionsGauge currently reports.
	publishedShortConnections = map[shortConnectionKey]int{}
)

func cableKey(cableDriverName, remoteClusterID string, family k8snet.IPFamily) string {
	return strings.Join([]string{cableDriverName, remoteClusterID, string(family)}, "/")
}

// setCableStatus returns the status it replaced and whether anything changed.
func setCableStatus(key string, current cableStatus) (cableStatus, bool) {
	cableStatusMutex.Lock()
	defer cableStatusMutex.Unlock()

	previous, exists := cableStatuses[key]

	// Stored unconditionally so the cable name cannot go stale, but kept out of the comparison: a
	// name-only change would otherwise delete the series just recorded for the cable.
	cableStatuses[key] = current

	if exists && previous.status == current.status && maps.Equal(previous.labels, current.labels) {
		return cableStatus{}, false
	}

	recordShortConnections()

	return previous, true
}

// deleteCableStatus reports whether the cable is the one recorded, and so whether its series should go.
func deleteCableStatus(key, cableName string, labels prometheus.Labels) bool {
	cableStatusMutex.Lock()
	defer cableStatusMutex.Unlock()

	existing, exists := cableStatuses[key]
	if !exists {
		return true
	}

	// A cable superseded by another to the same cluster is disconnected after its replacement was
	// installed. The replacement can carry the same labels, as the cable name is derived from the
	// private IP while the labels carry the public one, so compare the name too.
	if existing.cableName != cableName || !maps.Equal(existing.labels, labels) {
		return false
	}

	delete(cableStatuses, key)

	recordShortConnections()

	return true
}

// recordShortConnections recalculates the summary gauge as the number of connections per cable
// driver and status, updating the series that still apply in place so that a scrape never observes
// it partially rebuilt. It must be called with cableStatusMutex held.
func recordShortConnections() {
	counts := map[shortConnectionKey]int{}

	for _, cs := range cableStatuses {
		counts[shortConnectionKey{cableDriverName: cs.labels[cableDriverLabel], status: cs.status}]++
	}

	for key, count := range counts {
		shortConnectionsGauge.With(shortLabels(key)).Set(float64(count))
	}

	for key := range publishedShortConnections {
		if _, ok := counts[key]; !ok {
			shortConnectionsGauge.Delete(shortLabels(key))
		}
	}

	publishedShortConnections = counts
}

func shortLabels(key shortConnectionKey) prometheus.Labels {
	return prometheus.Labels{
		cableDriverLabel:       key.cableDriverName,
		connectionsStatusLabel: key.status,
	}
}

// withStatus copies the labels rather than adding to them, as they are retained in cableStatuses.
func withStatus(labels prometheus.Labels, status string) prometheus.Labels {
	labelsWithStatus := make(prometheus.Labels, len(labels)+1)
	maps.Copy(labelsWithStatus, labels)
	labelsWithStatus[connectionsStatusLabel] = status

	return labelsWithStatus
}

func getLabels(cableDriverName string, localEndpoint, remoteEndpoint *submv1.EndpointSpec, family k8snet.IPFamily) prometheus.Labels {
	return prometheus.Labels{
		cableDriverLabel:      cableDriverName,
		localClusterLabel:     localEndpoint.ClusterID,
		localHostnameLabel:    localEndpoint.Hostname,
		localEndpointIPLabel:  localEndpoint.GetPublicIP(family),
		remoteClusterLabel:    remoteEndpoint.ClusterID,
		remoteHostnameLabel:   remoteEndpoint.Hostname,
		remoteEndpointIPLabel: remoteEndpoint.GetPublicIP(family),
	}
}

func RecordRxBytes(cableDriverName string, localEndpoint, remoteEndpoint *submv1.EndpointSpec, bytes int, family k8snet.IPFamily) {
	rxGauge.With(getLabels(cableDriverName, localEndpoint, remoteEndpoint, family)).Set(float64(bytes))
}

func RecordTxBytes(cableDriverName string, localEndpoint, remoteEndpoint *submv1.EndpointSpec, bytes int, family k8snet.IPFamily) {
	txGauge.With(getLabels(cableDriverName, localEndpoint, remoteEndpoint, family)).Set(float64(bytes))
}

func RecordConnectionLatency(
	cableDriverName string,
	localEndpoint, remoteEndpoint *submv1.EndpointSpec,
	latencySeconds float64,
	family k8snet.IPFamily,
) {
	connectionLatencySecondsGauge.With(getLabels(cableDriverName, localEndpoint, remoteEndpoint, family)).Set(latencySeconds)
}

// RecordConnection and RecordDisconnected rely on the cable engine serializing their callers.
func RecordConnection(
	cableDriverName string,
	localEndpoint, remoteEndpoint *submv1.EndpointSpec,
	status string,
	isNew bool,
	family k8snet.IPFamily,
) {
	labels := getLabels(cableDriverName, localEndpoint, remoteEndpoint, family)

	if isNew {
		connectionEstablishedTimestampGauge.With(labels).Set(float64(time.Now().Unix()))
	}

	previous, changed := setCableStatus(cableKey(cableDriverName, remoteEndpoint.ClusterID, family),
		cableStatus{labels: labels, status: status, cableName: remoteEndpoint.CableName})

	connectionsGauge.With(withStatus(labels, status)).Set(1)

	if !changed || previous.labels == nil {
		return
	}

	// Removed after the current status is recorded, so that a scrape never sees the cable missing.
	if maps.Equal(previous.labels, labels) {
		connectionsGauge.Delete(withStatus(previous.labels, previous.status))
	} else {
		removeCableMetrics(previous.labels)
	}
}

func RecordDisconnected(cableDriverName string, localEndpoint, remoteEndpoint *submv1.EndpointSpec, family k8snet.IPFamily) {
	labels := getLabels(cableDriverName, localEndpoint, remoteEndpoint, family)

	if deleteCableStatus(cableKey(cableDriverName, remoteEndpoint.ClusterID, family), remoteEndpoint.CableName, labels) {
		removeCableMetrics(labels)
	}
}

func removeCableMetrics(labels prometheus.Labels) {
	connectionLatencySecondsGauge.Delete(labels)
	connectionEstablishedTimestampGauge.Delete(labels)
	rxGauge.Delete(labels)
	txGauge.Delete(labels)

	// The connections gauge carries an additional status label, so an exact match on the labels
	// above never removes its series.
	connectionsGauge.DeletePartialMatch(labels)
}
