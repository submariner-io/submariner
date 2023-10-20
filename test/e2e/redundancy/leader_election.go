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

package redundancy

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/slices"
	"github.com/submariner-io/admiral/pkg/util"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/tcp"
	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	subFramework "github.com/submariner-io/submariner/test/e2e/framework"
	coordinationv1 "k8s.io/api/coordination/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Leader election tests", func() {
	f := subFramework.NewFramework("leader-election")

	When("renewal of the leader lease fails", func() {
		It("should re-acquire the leader lease after the failure is cleared", func() {
			cluster := subFramework.FindClusterWithSingleGateway()
			if cluster == -1 {
				framework.Skipf("The test requires single gateway node in one of the test clusters...")
			}

			clusterName := framework.TestContext.ClusterIDs[cluster]

			gatewayPod := f.AwaitActiveGatewayPod(cluster, nil)
			Expect(gatewayPod).ToNot(BeNil(), "Did not find an active gateway pod")

			restartCount := gatewayPod.Status.ContainerStatuses[0].RestartCount

			gateway := f.AwaitGatewayWithStatus(cluster, gatewayPod.Spec.NodeName, subv1.HAStatusActive)

			By(fmt.Sprintf("Found submariner Gateway %q on cluster %q", gateway.Name, clusterName))

			By("Updating submariner-gateway Role to remove Lease update permission")

			updateLeaseUpdatePermission(cluster, gateway.Namespace, slices.Remove[string, string])

			DeferCleanup(func() {
				updateLeaseUpdatePermission(cluster, gateway.Namespace, slices.AppendIfNotPresent[string, string])
			})

			By(fmt.Sprintf("Ensure Gateway %q is updated to passive", gateway.Name))

			f.AwaitGatewaysWithStatus(cluster, subv1.HAStatusPassive)

			subFramework.VerifyDatapathConnectivity(tcp.ConnectivityTestParams{
				Framework:             f.Framework,
				FromCluster:           framework.ClusterA,
				FromClusterScheduling: framework.NonGatewayNode,
				ToCluster:             framework.ClusterB,
				ToClusterScheduling:   framework.NonGatewayNode,
				ToEndpointType:        defaultEndpointType(),
				Networking:            framework.PodNetworking,
			}, subFramework.GetGlobalnetEgressParams(subFramework.ClusterSelector))

			By("Updating submariner-gateway Role to add Lease update permission")

			updateLeaseUpdatePermission(cluster, gateway.Namespace, slices.AppendIfNotPresent[string, string])

			By(fmt.Sprintf("Ensure Gateway %q is updated to active", gateway.Name))

			f.AwaitGatewayWithStatus(cluster, gatewayPod.Spec.NodeName, subv1.HAStatusActive)

			subFramework.VerifyDatapathConnectivity(tcp.ConnectivityTestParams{
				Framework:             f.Framework,
				FromCluster:           framework.ClusterA,
				FromClusterScheduling: framework.GatewayNode,
				ToCluster:             framework.ClusterB,
				ToClusterScheduling:   framework.GatewayNode,
				ToEndpointType:        defaultEndpointType(),
				Networking:            framework.PodNetworking,
			}, subFramework.GetGlobalnetEgressParams(subFramework.ClusterSelector))

			gatewayPod, err := framework.KubeClients[cluster].CoreV1().Pods(gatewayPod.Namespace).Get(context.Background(),
				gatewayPod.Name, metav1.GetOptions{})
			Expect(err).To(Succeed())

			Expect(gatewayPod.Status.ContainerStatuses[0].RestartCount).To(Equal(restartCount),
				"Gateway pod %q was restarted", gatewayPod.Name)
		})
	})
})

func updateLeaseUpdatePermission(cluster framework.ClusterIndex, ns string,
	updateFn func([]string, string, func(string) string) ([]string, bool),
) {
	err := util.Update[*rbacv1.Role](context.Background(), resource.ForRole(framework.KubeClients[cluster], ns),
		&rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{Name: "submariner-gateway"},
		},
		func(role *rbacv1.Role) (*rbacv1.Role, error) {
			for i := range role.Rules {
				if role.Rules[i].APIGroups[0] == coordinationv1.GroupName && role.Rules[i].Resources[0] == "leases" {
					role.Rules[i].Verbs, _ = updateFn(role.Rules[i].Verbs, "update", func(e string) string {
						return e
					})

					break
				}
			}

			return role, nil
		})
	Expect(err).To(Succeed())
}
