/*
© 2021 Red Hat, Inc. and others

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
	"fmt"

	. "github.com/onsi/ginkgo"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/tcp"

	subFramework "github.com/submariner-io/submariner/test/e2e/framework"
)

var _ = Describe("[redundancy] Route Agent restart tests", func() {
	f := subFramework.NewFramework("route-agent-restart")

	When("a route agent pod running on a gateway node is restarted", func() {
		It("should start a new route agent pod and be able to connect from another cluster", func() {
			testRouteAgentRestart(f, true)
		})
	})

	When("a route agent pod running on a non-gateway node is restarted", func() {
		It("should start a new route agent pod and be able to connect from another cluster", func() {
			testRouteAgentRestart(f, false)
		})
	})
})

func testRouteAgentRestart(f *subFramework.Framework, onGateway bool) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	nodes := f.FindNodesByGatewayLabel(framework.ClusterA, onGateway)
	By(fmt.Sprintf("Found node %q on %q", nodes[0].Name, clusterAName))
	node := nodes[0]

	routeAgentPod := f.AwaitRouteAgentPodOnNode(framework.ClusterA, node.Name, "")
	By(fmt.Sprintf("Found route agent pod %q on node %q", routeAgentPod.Name, node.Name))

	By(fmt.Sprintf("Deleting route agent pod %q", routeAgentPod.Name))
	f.DeletePod(framework.ClusterA, routeAgentPod.Name, framework.TestContext.SubmarinerNamespace)

	newRouteAgentPod := f.AwaitRouteAgentPodOnNode(framework.ClusterA, node.Name, routeAgentPod.UID)
	By(fmt.Sprintf("Found new route agent pod %q on node %q", newRouteAgentPod.Name, node.Name))

	By(fmt.Sprintf("Verifying TCP connectivity from gateway node on %q to gateway node on %q", clusterBName, clusterAName))
	tcp.RunConnectivityTest(tcp.ConnectivityTestParams{
		Framework:             f.Framework,
		FromCluster:           framework.ClusterB,
		FromClusterScheduling: framework.GatewayNode,
		ToCluster:             framework.ClusterA,
		ToClusterScheduling:   framework.GatewayNode,
		ToEndpointType:        defaultEndpointType(),
	})

	By(fmt.Sprintf("Verifying TCP connectivity from non-gateway node on %q to non-gateway node on %q", clusterBName, clusterAName))
	tcp.RunConnectivityTest(tcp.ConnectivityTestParams{
		Framework:             f.Framework,
		FromCluster:           framework.ClusterB,
		FromClusterScheduling: framework.NonGatewayNode,
		ToCluster:             framework.ClusterA,
		ToClusterScheduling:   framework.NonGatewayNode,
		ToEndpointType:        defaultEndpointType(),
	})
}
