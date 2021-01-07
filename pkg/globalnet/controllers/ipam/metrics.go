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

package ipam

import "github.com/prometheus/client_golang/prometheus"

const (
	cidrLabel = "cidr"
)

var (
	globalIPsAvailabilityGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "global_IP_availability",
			Help: "Count of available global IPs per CIDR",
		},
		[]string{
			cidrLabel,
		},
	)
	globalIPsAllocatedGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "global_IP_allocated",
			Help: "Count of global IPs allocated for Pods/Services per CIDR",
		},
		[]string{
			cidrLabel,
		},
	)
)

func init() {
	prometheus.MustRegister(globalIPsAvailabilityGauge, globalIPsAllocatedGauge)
}

func RecordAllocateGlobalIP(cidr string) {
	globalIPsAllocatedGauge.With(prometheus.Labels{cidrLabel: cidr}).Inc()
	globalIPsAvailabilityGauge.With(prometheus.Labels{cidrLabel: cidr}).Dec()
}

func RecordDeallocateGlobalIP(cidr string) {
	globalIPsAllocatedGauge.With(prometheus.Labels{cidrLabel: cidr}).Dec()
	globalIPsAvailabilityGauge.With(prometheus.Labels{cidrLabel: cidr}).Inc()
}

func RecordAvailability(cidr string, count int) {
	globalIPsAvailabilityGauge.With(prometheus.Labels{cidrLabel: cidr}).Set(float64(count))
}
