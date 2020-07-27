package redundancy

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/tcp"
	v1 "k8s.io/api/core/v1"

	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	subFramework "github.com/submariner-io/submariner/test/e2e/framework"
)

var _ = Describe("[redundancy] Gateway fail-over tests", func() {
	f := subFramework.NewFramework("gateway-redundancy")

	// After each test, we make sure that the system again has a single gateway, the active one
	AfterEach(f.GatewayCleanup)

	When("any gateway node is configured", func() {
		It("should be reported to the Gateway API", func() {
			testBasicGatewayReporting(f)
		})
	})

	When("one gateway node is configured and the submariner engine pod fails", func() {
		It("should start a new submariner engine pod and be able to connect from another cluster", func() {
			testEnginePodRestartScenario(f)
		})
	})

	When("a new node is labeled as a gateway node and the label on the existing gateway node is removed", func() {
		It("should start a submariner engine on the new gateway node and be able to connect from another cluster", func() {
			testGatewayFailOverScenario(f)
		})
	})
})

func testBasicGatewayReporting(f *subFramework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]

	By(fmt.Sprintf("Ensuring that only one gateway reports as active %q", clusterAName))

	activeGateways := f.AwaitGatewaysWithStatus(framework.ClusterA, subv1.HAStatusActive)

	Expect(activeGateways).To(HaveLen(1))

	By(fmt.Sprintf("Ensuring that the gateway %q is reporting connections", activeGateways[0].Name))
	f.AwaitGatewayFullyConnected(framework.ClusterA, activeGateways[0].Name)
}

func testEnginePodRestartScenario(f *subFramework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Sanity check - ensuring there's only one gateway node on %q", clusterAName))

	gatewayNodes := f.FindNodesByGatewayLabel(framework.ClusterA, true)
	Expect(gatewayNodes).To(HaveLen(1), fmt.Sprintf("Expected only one gateway node on %q", clusterAName))

	enginePod := f.AwaitSubmarinerEnginePod(framework.ClusterA)
	By(fmt.Sprintf("Found submariner engine pod %q on %q", enginePod.Name, clusterAName))

	By(fmt.Sprintf("Ensuring that the gateway reports as active on %q", clusterAName))

	activeGateway := f.AwaitGatewayFullyConnected(framework.ClusterA, gatewayNodes[0].Name)

	By(fmt.Sprintf("Deleting submariner engine pod and gateway entries %q", enginePod.Name))
	f.DeletePod(framework.ClusterA, enginePod.Name, framework.TestContext.SubmarinerNamespace)

	newEnginePod := f.AwaitSubmarinerEnginePod(framework.ClusterA)
	By(fmt.Sprintf("Found new submariner engine pod %q", newEnginePod.Name))

	By(fmt.Sprintf("Waiting for the gateway to be up and connected %q", newEnginePod.Name))
	f.AwaitGatewayFullyConnected(framework.ClusterA, activeGateway.Name)

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

func defaultEndpointType() tcp.EndpointType {
	if framework.TestContext.GlobalnetEnabled {
		return tcp.GlobalIP
	}

	return tcp.PodIP
}

func testGatewayFailOverScenario(f *subFramework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	gatewayNodes := f.FindNodesByGatewayLabel(framework.ClusterA, true)
	Expect(gatewayNodes).To(HaveLen(1), fmt.Sprintf("Expected only one gateway node on %q", clusterAName))

	initialGatewayNode := gatewayNodes[0]
	By(fmt.Sprintf("Found gateway node %q on %q", initialGatewayNode.Name, clusterAName))

	nonGatewayNodes := f.FindNodesByGatewayLabel(framework.ClusterA, false)
	Expect(len(nonGatewayNodes) > 0).To(BeTrue(), fmt.Sprintf("Expected at least one non-gateway node on %q", clusterAName))

	initialNonGatewayNode := nonGatewayNodes[0]
	By(fmt.Sprintf("Found non-gateway node %q on %q", initialNonGatewayNode.Name, clusterAName))

	enginePod := f.AwaitSubmarinerEnginePod(framework.ClusterA)
	By(fmt.Sprintf("Found submariner engine pod %q on %q", enginePod.Name, clusterAName))

	submEndpoint := f.AwaitSubmarinerEndpoint(framework.ClusterA, subFramework.NoopCheckEndpoint)
	By(fmt.Sprintf("Found submariner endpoint for %q: %#v", clusterAName, submEndpoint))

	By(fmt.Sprintf("Setting the gateway label for node %q to true", initialNonGatewayNode.Name))
	f.SetGatewayLabelOnNode(framework.ClusterA, initialNonGatewayNode.Name, true)

	By(fmt.Sprintf("Ensuring that two Gateways become available in cluster %q", clusterAName))

	f.AwaitGatewayFullyConnected(framework.ClusterA, initialGatewayNode.Name)
	gwPassive := f.AwaitGatewayWithStatus(framework.ClusterA, initialNonGatewayNode.Name, subv1.HAStatusPassive)
	Expect(gwPassive.Status.Connections).To(BeEmpty(), "The passive gateway must have no connections")

	// Start watching the API for Gateway deletions
	gwInformer, stopInformer := f.GetGatewayInformer(framework.ClusterA)
	defer close(stopInformer)

	deleteCh := subFramework.GetDeletionChannel(gwInformer)

	// Set the gateway label for the active gateway worker node to false so the submariner engine pod will be
	// terminated.
	By(fmt.Sprintf("Setting the gateway label for node %q to false", initialGatewayNode.Name))
	f.SetGatewayLabelOnNode(framework.ClusterA, initialGatewayNode.Name, false)

	By(fmt.Sprintf("Verifying that the gateway %q was deleted", initialGatewayNode.Name))
	Eventually(deleteCh, framework.TestContext.OperationTimeout).Should(Receive(Equal(initialGatewayNode.Name)))

	// Ensure the new engine pod is started before we run the connectivity tests to eliminate possible timing issue where,
	// after deleting the old pod, we actually run the connectivity test against the old engine instance before k8s has
	// a chance to react to stop the process/container etc.
	var newEnginePod *v1.Pod
	for retries := 1; retries < 10; retries++ {
		newEnginePod = f.AwaitSubmarinerEnginePod(framework.ClusterA)
		if newEnginePod.Spec.NodeName == initialGatewayNode.Name {
			time.Sleep(5 * time.Second)
		} else {
			break
		}
	}
	Expect(newEnginePod.Spec.NodeName).To(Equal(initialNonGatewayNode.Name),
		"The new engine pod is not running on the expected node")
	By(fmt.Sprintf("Found new submariner engine pod %q", newEnginePod.Name))

	By(fmt.Sprintf("Waiting for the new pod %q to report as active and fully connected", newEnginePod.Name))
	f.AwaitGatewayFullyConnected(framework.ClusterA, newEnginePod.Spec.NodeName)

	// Verify a new Endpoint instance is created by the new engine instance. This is a bit whitebox but it's a ssanity check
	// and also gives it a bit more of a cushion to avoid premature timeout in the connectivity test.
	newSubmEndpoint := f.AwaitNewSubmarinerEndpoint(framework.ClusterA, submEndpoint.ObjectMeta.UID)
	By(fmt.Sprintf("Found new submariner endpoint for %q: %#v", clusterAName, newSubmEndpoint))

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
