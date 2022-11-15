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

package dataplane

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	subFramework "github.com/submariner-io/submariner/test/e2e/framework"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("[dataplane] Gateway status reporting", func() {
	f := subFramework.NewFramework("dataplane-gateway-status")

	When("a gateway node is configured", func() {
		It("should correctly report its status and connection information", func() {
			clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]

			By(fmt.Sprintf("Ensuring that only one gateway reports as active on %q", clusterAName))

			activeGateways := f.AwaitGatewaysWithStatus(framework.ClusterA, submarinerv1.HAStatusActive)
			Expect(activeGateways).To(HaveLen(1))

			name := activeGateways[0].Name
			otherCluster := framework.TestContext.ClusterIDs[framework.ClusterB]

			By(fmt.Sprintf("Ensuring that gateway %q reports connection information for cluster %q", name, otherCluster))

			gwClient := subFramework.SubmarinerClients[framework.ClusterA].SubmarinerV1().Gateways(
				framework.TestContext.SubmarinerNamespace)
			framework.AwaitUntil(fmt.Sprintf("await active connection on Gateway %q", name),
				func() (interface{}, error) {
					resGw, err := gwClient.Get(context.TODO(), name, metav1.GetOptions{})
					if apierrors.IsNotFound(err) {
						return nil, nil //nolint:nilnil // Returning nil value is intentional
					}
					return resGw, err
				},
				func(result interface{}) (bool, string, error) {
					if result == nil {
						return false, "gateway not found", nil
					}

					return verifyGateway(result.(*submarinerv1.Gateway), otherCluster)
				})
		})
	})
})

func verifyGateway(gw *submarinerv1.Gateway, otherCluster string) (bool, string, error) {
	if len(gw.Status.Connections) == 0 {
		return false, "Gateway has no connections", nil
	}

	for i := range gw.Status.Connections {
		if gw.Status.Connections[i].Endpoint.ClusterID != otherCluster {
			continue
		}

		if gw.Status.Connections[i].Status != submarinerv1.Connected {
			return false, fmt.Sprintf("Cluster %q is not connected: Status: %q, Message: %q",
				gw.Status.Connections[i].Endpoint.ClusterID, gw.Status.Connections[i].Status, gw.Status.Connections[i].StatusMessage), nil
		}

		if gw.Status.LocalEndpoint.HealthCheckIP != "" && gw.Status.Connections[i].Endpoint.HealthCheckIP != "" {
			if gw.Status.Connections[i].LatencyRTT == nil {
				return false, fmt.Sprintf("Connection for cluster %q has no LatencyRTT information", otherCluster), nil
			}

			if gw.Status.Connections[i].LatencyRTT.Average == "" {
				return false, fmt.Sprintf("Connection for cluster %q is missing Average RTT data", otherCluster), nil
			}

			if gw.Status.Connections[i].LatencyRTT.Last == "" {
				return false, fmt.Sprintf("Connection for cluster %q is missing Last RTT data", otherCluster), nil
			}

			if gw.Status.Connections[i].LatencyRTT.Min == "" {
				return false, fmt.Sprintf("Connection for cluster %q is missing Min RTT data", otherCluster), nil
			}

			if gw.Status.Connections[i].LatencyRTT.Max == "" {
				return false, fmt.Sprintf("Connection for cluster %q is missing Max RTT data", otherCluster), nil
			}

			if gw.Status.Connections[i].LatencyRTT.StdDev == "" {
				return false, fmt.Sprintf("Connection for cluster %q is missing StdDev RTT data", otherCluster), nil
			}
		}

		return true, "", nil
	}

	return false, fmt.Sprintf("Connection for cluster %q was not found", otherCluster), nil
}
