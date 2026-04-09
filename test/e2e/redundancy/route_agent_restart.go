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
	"github.com/submariner-io/shipyard/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/tcp"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	subDataplane "github.com/submariner-io/submariner/test/e2e/dataplane"
	subFramework "github.com/submariner-io/submariner/test/e2e/framework"
	"github.com/submariner-io/submariner/test/e2e/labels"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8snet "k8s.io/utils/net"
)

var _ = Describe("Route Agent restart tests", Label(labels.Redundancy), func() {
	f := subFramework.NewFramework("route-agent-restart")

	var supportedFamilies []k8snet.IPFamily

	BeforeEach(func(ctx context.Context) {
		supportedFamilies = subDataplane.GetActualIPFamilies(
			f.DetermineIPFamilyType(ctx, framework.ClusterA),
			f.DetermineIPFamilyType(ctx, framework.ClusterB),
		)
	})

	When("a route agent pod running on a gateway node is restarted", func() {
		It("should start a new route agent pod and be able to connect from another cluster", func(ctx context.Context) {
			testRouteAgentRestart(ctx, f, true, supportedFamilies)
		})
	})

	When("a route agent pod running on a non-gateway node is restarted", func() {
		It("should start a new route agent pod and be able to connect from another cluster", func(ctx context.Context) {
			testRouteAgentRestart(ctx, f, false, supportedFamilies)
		})
	})
})

func testRouteAgentRestart(ctx context.Context, f *subFramework.Framework, onGateway bool, supportedFamilies []k8snet.IPFamily) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	var nodes []v1.Node
	if onGateway {
		nodes = framework.FindGatewayNodes(ctx, framework.ClusterA)
	} else {
		nodes = framework.FindNonGatewayNodes(ctx, framework.ClusterA)
	}

	if len(nodes) == 0 && !onGateway {
		framework.Skipf("Skipping the test as cluster %q doesn't have any suitable non-gateway nodes...", clusterAName)
		return
	}

	if !onGateway && framework.TestContext.SkipIntraClusterConnectivityTests {
		framework.Skipf("Skipping intra-cluster connectivity test")
		return
	}

	framework.By(fmt.Sprintf("Found node %q on %q", nodes[0].Name, clusterAName))
	node := nodes[0]

	routeAgentPod := f.AwaitRouteAgentPodOnNode(ctx, framework.ClusterA, node.Name, "")
	framework.By(fmt.Sprintf("Found route agent pod %q on node %q", routeAgentPod.Name, node.Name))

	assertRouteAgentResource(ctx, framework.ClusterA, node.Name, routeAgentPod.Name)

	framework.By(fmt.Sprintf("Deleting route agent pod %q", routeAgentPod.Name))
	f.DeletePod(ctx, framework.ClusterA, routeAgentPod.Name, framework.TestContext.SubmarinerNamespace)

	newRouteAgentPod := f.AwaitRouteAgentPodOnNode(ctx, framework.ClusterA, node.Name, routeAgentPod.UID)
	framework.By(fmt.Sprintf("Found new route agent pod %q on node %q", newRouteAgentPod.Name, node.Name))

	framework.By(fmt.Sprintf("Verifying TCP connectivity from gateway node on %q to gateway node on %q", clusterBName, clusterAName))

	for _, ipFamily := range supportedFamilies {
		subFramework.VerifyDatapathConnectivity(ctx, &tcp.ConnectivityTestParams{
			Framework:             f.Framework,
			FromCluster:           framework.ClusterB,
			FromClusterScheduling: framework.GatewayNode,
			ToCluster:             framework.ClusterA,
			ToClusterScheduling:   framework.GatewayNode,
			ToEndpointType:        defaultEndpointType(),
			IPFamily:              ipFamily,
		}, subFramework.GetGlobalnetEgressParams(subFramework.ClusterSelector))
	}

	if framework.TestContext.SkipIntraClusterConnectivityTests {
		framework.Skipf("Skipping non-gateway TCP test as intra-cluster routing is disabled")
		return
	}

	framework.By(fmt.Sprintf("Verifying TCP connectivity from non-gateway node on %q to non-gateway node on %q", clusterBName, clusterAName))

	for _, ipFamily := range supportedFamilies {
		subFramework.VerifyDatapathConnectivity(ctx, &tcp.ConnectivityTestParams{
			Framework:             f.Framework,
			FromCluster:           framework.ClusterB,
			FromClusterScheduling: framework.NonGatewayNode,
			ToCluster:             framework.ClusterA,
			ToClusterScheduling:   framework.NonGatewayNode,
			ToEndpointType:        defaultEndpointType(),
			IPFamily:              ipFamily,
		}, subFramework.GetGlobalnetEgressParams(subFramework.ClusterSelector))
	}
}

func assertRouteAgentResource(ctx context.Context, cluster framework.ClusterIndex, name, ownerName string) {
	raClient := framework.DynClients[cluster].Resource(submarinerv1.SchemeGroupVersion.WithResource("routeagents")).Namespace(
		framework.TestContext.SubmarinerNamespace)

	routeAgent := framework.AwaitUntil(ctx, fmt.Sprintf("await RouteAgent %q", name),
		func(ctx context.Context) (*unstructured.Unstructured, error) {
			ra, err := raClient.Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil, nil //nolint:nilnil // OK
			}

			return ra, err
		},
		func(ra *unstructured.Unstructured) (bool, string, error) {
			return ra != nil, "RouteAgent not found yet", nil
		})

	Expect(routeAgent.GetOwnerReferences()).To(HaveLen(1))
	Expect(routeAgent.GetOwnerReferences()[0].Name).To(Equal(ownerName))
}
