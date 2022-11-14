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

package metrics

import "github.com/prometheus/client_golang/prometheus"

const (
	cidrLabel = "cidr"
)

var (
	globalIPsAvailabilityGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "submariner_global_IP_availability",
			Help: "Count of available global IPs per CIDR",
		},
		[]string{
			cidrLabel,
		},
	)
	globalIPsAllocatedGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "submariner_global_IP_allocated",
			Help: "Count of global IPs allocated for Pods/Services per CIDR",
		},
		[]string{
			cidrLabel,
		},
	)
	globalEgressIPsAllocatedGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "submariner_global_egress_IP_allocated",
			Help: "Count of global Egress IPs allocated for Pods/Services per CIDR",
		},
		[]string{
			cidrLabel,
		},
	)
	clusterGlobalEgressIPsAllocatedGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "submariner_cluster_global_egress_IP_allocated",
			Help: "Count of global Egress IPs allocated for clusters per CIDR",
		},
		[]string{
			cidrLabel,
		},
	)
	globalIngressIPsAllocatedGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "submariner_global_ingress_IP_allocated",
			Help: "Count of global Ingress IPs allocated for Pods/Services per CIDR",
		},
		[]string{
			cidrLabel,
		},
	)
)

func init() {
	prometheus.MustRegister(globalIPsAvailabilityGauge, globalIPsAllocatedGauge, globalEgressIPsAllocatedGauge,
		clusterGlobalEgressIPsAllocatedGauge, globalIngressIPsAllocatedGauge)
}

func RecordAllocateGlobalIP(cidr string) {
	globalIPsAllocatedGauge.With(prometheus.Labels{cidrLabel: cidr}).Inc()
	globalIPsAvailabilityGauge.With(prometheus.Labels{cidrLabel: cidr}).Dec()
}

func RecordAllocateGlobalIPs(cidr string, count int) {
	globalIPsAllocatedGauge.With(prometheus.Labels{cidrLabel: cidr}).Add(float64(count))
	globalIPsAvailabilityGauge.With(prometheus.Labels{cidrLabel: cidr}).Sub(float64(count))
}

func RecordAllocateGlobalEgressIPs(cidr string, count int) {
	globalEgressIPsAllocatedGauge.With(prometheus.Labels{cidrLabel: cidr}).Add(float64(count))
}

func RecordAllocateClusterGlobalEgressIPs(cidr string, count int) {
	clusterGlobalEgressIPsAllocatedGauge.With(prometheus.Labels{cidrLabel: cidr}).Add(float64(count))
}

func RecordAllocateGlobalIngressIPs(cidr string, count int) {
	globalIngressIPsAllocatedGauge.With(prometheus.Labels{cidrLabel: cidr}).Add(float64(count))
}

func RecordDeallocateGlobalIP(cidr string) {
	globalIPsAllocatedGauge.With(prometheus.Labels{cidrLabel: cidr}).Dec()
	globalIPsAvailabilityGauge.With(prometheus.Labels{cidrLabel: cidr}).Inc()
}

func RecordDeallocateGlobalEgressIPs(cidr string, count int) {
	globalEgressIPsAllocatedGauge.With(prometheus.Labels{cidrLabel: cidr}).Sub(float64(count))
}

func RecordDeallocateClusterGlobalEgressIPs(cidr string, count int) {
	clusterGlobalEgressIPsAllocatedGauge.With(prometheus.Labels{cidrLabel: cidr}).Sub(float64(count))
}

func RecordDeallocateGlobalIngressIPs(cidr string, count int) {
	globalIngressIPsAllocatedGauge.With(prometheus.Labels{cidrLabel: cidr}).Sub(float64(count))
}

func RecordAvailability(cidr string, count int) {
	globalIPsAvailabilityGauge.With(prometheus.Labels{cidrLabel: cidr}).Set(float64(count))
}
