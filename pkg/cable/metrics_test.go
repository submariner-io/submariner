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

package cable_test

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	submv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	k8snet "k8s.io/utils/net"
)

const (
	testCableDriver   = "test-driver"
	testLocalCluster  = "local-cluster"
	testLocalHostname = "local-host"
	testLocalIP       = "10.0.0.1"
)

const (
	connectionsHeader = "# HELP submariner_connections Number of connections and corresponding status " +
		"(by cable driver and cable)\n# TYPE submariner_connections gauge\n"
	shortConnectionsHeader = "# HELP submariner_connections_short Summary of connections and corresponding status " +
		"without cable information\n# TYPE submariner_connections_short gauge\n"
)

var _ = Describe("Connection metrics", func() {
	var (
		localEndpoint *submv1.EndpointSpec
		endpoints     []*submv1.EndpointSpec
	)

	BeforeEach(func() {
		localEndpoint = &submv1.EndpointSpec{
			ClusterID: testLocalCluster,
			Hostname:  testLocalHostname,
			PublicIPs: []string{testLocalIP},
		}

		endpoints = nil
	})

	// The metrics are registered with the default registry, so remove everything recorded by a spec
	// before the next one runs.
	AfterEach(func() {
		for _, ep := range endpoints {
			cable.RecordDisconnected(testCableDriver, localEndpoint, ep, k8snet.IPv4)
		}

		Expect(gatheredCount("submariner_connections")).To(BeZero())
		Expect(gatheredCount("submariner_connections_short")).To(BeZero())
	})

	recordConnection := func(remoteEndpoint *submv1.EndpointSpec, status string) {
		endpoints = append(endpoints, remoteEndpoint)
		cable.RecordConnection(testCableDriver, localEndpoint, remoteEndpoint, status, false, k8snet.IPv4)
	}

	When("the status of a connection changes", func() {
		It("should only report its current status", func() {
			remoteEndpoint := newRemoteEndpoint("remote-cluster", "10.1.0.1")

			recordConnection(remoteEndpoint, string(submv1.Connected))
			recordConnection(remoteEndpoint, string(submv1.ConnectionError))

			expectConnections(connectionMetric(remoteEndpoint, string(submv1.ConnectionError)))
			expectShortConnections(shortConnectionMetric(string(submv1.ConnectionError), 1))
		})
	})

	When("connections to several clusters are recorded", func() {
		It("should report the status of each and summarize them per status", func() {
			remoteEndpoint1 := newRemoteEndpoint("remote-cluster1", "10.1.0.1")
			remoteEndpoint2 := newRemoteEndpoint("remote-cluster2", "10.2.0.1")

			recordConnection(remoteEndpoint1, string(submv1.Connected))
			recordConnection(remoteEndpoint2, string(submv1.ConnectionError))

			expectConnections(connectionMetric(remoteEndpoint1, string(submv1.Connected)),
				connectionMetric(remoteEndpoint2, string(submv1.ConnectionError)))
			expectShortConnections(shortConnectionMetric(string(submv1.Connected), 1),
				shortConnectionMetric(string(submv1.ConnectionError), 1))

			By("Recovering the connection in error")

			recordConnection(remoteEndpoint2, string(submv1.Connected))

			expectConnections(connectionMetric(remoteEndpoint1, string(submv1.Connected)),
				connectionMetric(remoteEndpoint2, string(submv1.Connected)))
			expectShortConnections(shortConnectionMetric(string(submv1.Connected), 2))
		})
	})

	When("a cable is replaced by one to the same cluster", func() {
		It("should stop reporting the cable it replaced", func() {
			remoteEndpoint := newRemoteEndpoint("remote-cluster", "10.1.0.1")

			recordConnection(remoteEndpoint, string(submv1.Connected))

			By("Recording a cable to the same cluster, as happens when its gateway fails over")

			failedOver := newRemoteEndpoint("remote-cluster", "10.1.0.2")
			failedOver.Hostname = "remote-cluster-host2"

			recordConnection(failedOver, string(submv1.Connected))

			expectConnections(connectionMetric(failedOver, string(submv1.Connected)))
			expectShortConnections(shortConnectionMetric(string(submv1.Connected), 1))

			By("Disconnecting the cable it replaced, which the cable engine does after the fact")

			cable.RecordDisconnected(testCableDriver, localEndpoint, remoteEndpoint, k8snet.IPv4)

			expectConnections(connectionMetric(failedOver, string(submv1.Connected)))
			expectShortConnections(shortConnectionMetric(string(submv1.Connected), 1))
		})
	})

	When("a cable is replaced by one carrying the same labels", func() {
		It("should keep reporting the replacement when the cable it replaced is disconnected", func() {
			remoteEndpoint := newRemoteEndpoint("remote-cluster", "10.1.0.1")
			remoteEndpoint.CableName = "submariner-cable-remote-cluster-192-168-1-1"

			recordConnection(remoteEndpoint, string(submv1.Connected))

			By("Recording a cable to the same endpoint under a new cable name")

			replacement := newRemoteEndpoint("remote-cluster", "10.1.0.1")
			replacement.CableName = "submariner-cable-remote-cluster-192-168-1-2"

			recordConnection(replacement, string(submv1.Connected))

			By("Disconnecting the cable it replaced")

			cable.RecordDisconnected(testCableDriver, localEndpoint, remoteEndpoint, k8snet.IPv4)

			expectConnections(connectionMetric(replacement, string(submv1.Connected)))
			expectShortConnections(shortConnectionMetric(string(submv1.Connected), 1))
		})
	})

	When("a connection is recorded repeatedly with an unchanged status", func() {
		It("should not disturb what is reported for it", func() {
			remoteEndpoint := newRemoteEndpoint("remote-cluster", "10.1.0.1")

			recordConnection(remoteEndpoint, string(submv1.Connected))

			done := make(chan struct{})

			go func() {
				defer GinkgoRecover()
				defer close(done)

				for range 1000 {
					cable.RecordConnection(testCableDriver, localEndpoint, remoteEndpoint, string(submv1.Connected),
						false, k8snet.IPv4)
				}
			}()

			for {
				select {
				case <-done:
					return
				default:
					Expect(gatheredCount("submariner_connections")).To(Equal(1))
					Expect(gatheredCount("submariner_connections_short")).To(Equal(1))
				}
			}
		})
	})

	When("the status of a connection changes while it is scraped", func() {
		It("should never report it as absent", func() {
			remoteEndpoint := newRemoteEndpoint("remote-cluster", "10.1.0.1")

			recordConnection(remoteEndpoint, string(submv1.Connected))

			done := make(chan struct{})

			go func() {
				defer GinkgoRecover()
				defer close(done)

				for i := range 1000 {
					status := string(submv1.Connected)
					if i%2 == 0 {
						status = string(submv1.ConnectionError)
					}

					cable.RecordConnection(testCableDriver, localEndpoint, remoteEndpoint, status, false, k8snet.IPv4)
				}
			}()

			for {
				select {
				case <-done:
					return
				default:
					Expect(gatheredCount("submariner_connections")).ToNot(BeZero())
					Expect(gatheredCount("submariner_connections_short")).ToNot(BeZero())
				}
			}
		})
	})

	When("a connection is disconnected", func() {
		It("should stop reporting it", func() {
			remoteEndpoint1 := newRemoteEndpoint("remote-cluster1", "10.1.0.1")
			remoteEndpoint2 := newRemoteEndpoint("remote-cluster2", "10.2.0.1")

			recordConnection(remoteEndpoint1, string(submv1.ConnectionError))
			recordConnection(remoteEndpoint2, string(submv1.Connected))

			cable.RecordDisconnected(testCableDriver, localEndpoint, remoteEndpoint1, k8snet.IPv4)

			expectConnections(connectionMetric(remoteEndpoint2, string(submv1.Connected)))
			expectShortConnections(shortConnectionMetric(string(submv1.Connected), 1))
		})
	})
})

func newRemoteEndpoint(clusterID, publicIP string) *submv1.EndpointSpec {
	return &submv1.EndpointSpec{
		ClusterID: clusterID,
		Hostname:  clusterID + "-host",
		PublicIPs: []string{publicIP},
	}
}

func connectionMetric(remoteEndpoint *submv1.EndpointSpec, status string) string {
	return fmt.Sprintf("submariner_connections{cable_driver=%q,local_cluster=%q,local_endpoint_ip=%q,local_hostname=%q,"+
		"remote_cluster=%q,remote_endpoint_ip=%q,remote_hostname=%q,status=%q} 1\n",
		testCableDriver, testLocalCluster, testLocalIP, testLocalHostname,
		remoteEndpoint.ClusterID, remoteEndpoint.GetPublicIP(k8snet.IPv4), remoteEndpoint.Hostname, status)
}

func shortConnectionMetric(status string, count int) string {
	return fmt.Sprintf("submariner_connections_short{cable_driver=%q,status=%q} %d\n", testCableDriver, status, count)
}

func expectConnections(metrics ...string) {
	GinkgoHelper()
	expectMetrics("submariner_connections", connectionsHeader, metrics)
}

func expectShortConnections(metrics ...string) {
	GinkgoHelper()
	expectMetrics("submariner_connections_short", shortConnectionsHeader, metrics)
}

func expectMetrics(name, header string, metrics []string) {
	GinkgoHelper()
	Expect(testutil.GatherAndCompare(prometheus.DefaultGatherer,
		strings.NewReader(header+strings.Join(metrics, "")), name)).To(Succeed())
}

func gatheredCount(name string) int {
	count, err := testutil.GatherAndCount(prometheus.DefaultGatherer, name)
	Expect(err).To(Succeed())

	return count
}
