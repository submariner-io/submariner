package redundancy

import (
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/test/e2e/framework"
	"github.com/submariner-io/submariner/test/e2e/tcp"
	appsv1 "k8s.io/api/apps/v1"
)

var _ = PDescribe("[redundancy] Gateway fail-over tests", func() {
	f := framework.NewDefaultFramework("gateway-redundancy")

	When("one gateway node is configured and the submariner engine pod fails", func() {
		It("should start a new submariner engine pod and be able to connect from another cluster", func() {
			testOneGatewayNode(f)
		})
	})

	When("two gateway nodes are configured with one submariner engine replica and the gateway node fails", func() {
		It("should start a new submariner engine pod on the second gateway node and be able to connect from another cluster", func() {
			testTwoGatewayNodesWithOneReplica(f)
		})
	})

	When("two gateway nodes are configured with two submariner engine replicas and the active gateway node fails", func() {
		var deployment *appsv1.Deployment

		BeforeEach(func() {
			deployment = f.FindSubmarinerEngineDeployment(framework.ClusterA)
		})

		It("should fail over to the second submariner engine running on the second gateway node and be able to connect from another cluster", func() {
			testTwoGatewayNodesWithTwoReplicas(f, deployment)
		})

		AfterEach(func() {
			f.SetReplicas(framework.ClusterA, deployment, 1)
		})
	})
})

func testOneGatewayNode(f *framework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	By(fmt.Sprintf("Sanity check - ensuring there's only one gateway node on %q", clusterAName))
	gatewayNodes := f.FindNodesByGatewayLabel(framework.ClusterA, true)
	Expect(gatewayNodes).To(HaveLen(1), fmt.Sprintf("Expected only one gateway node on %q", clusterAName))

	enginePod := f.AwaitSubmarinerEnginePod(framework.ClusterA)
	By(fmt.Sprintf("Found submariner engine pod %q on %q", enginePod.Name, clusterAName))

	By(fmt.Sprintf("Deleting submariner engine pod %q", enginePod.Name))
	f.DeletePod(framework.ClusterA, enginePod.Name, framework.TestContext.SubmarinerNamespace)

	newEnginePod := f.AwaitSubmarinerEnginePod(framework.ClusterA)
	By(fmt.Sprintf("Found new submariner engine pod %q", newEnginePod.Name))

	By(fmt.Sprintf("Verifying TCP connectivity from gateway node on %q to gateway node on %q", clusterBName, clusterAName))
	tcp.RunConnectivityTest(f, false, framework.PodNetworking, framework.GatewayNode, framework.GatewayNode, framework.ClusterA, framework.ClusterB)

	By(fmt.Sprintf("Verifying TCP connectivity from non-gateway node on %q to non-gateway node on %q", clusterBName, clusterAName))
	tcp.RunConnectivityTest(f, false, framework.PodNetworking, framework.NonGatewayNode, framework.NonGatewayNode, framework.ClusterA, framework.ClusterB)
}

func testTwoGatewayNodesWithOneReplica(f *framework.Framework) {
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

	submEndpoint := f.AwaitSubmarinerEndpoint(framework.ClusterA, framework.NoopCheckEndpoint)
	By(fmt.Sprintf("Found submariner endpoint for %q: %#v", clusterAName, submEndpoint))

	By(fmt.Sprintf("Setting the gateway label for node %q to true", initialNonGatewayNode.Name))
	f.SetGatewayLabelOnNode(framework.ClusterA, initialNonGatewayNode.Name, true)

	// Set the gateway label for the active gateway worker node to false so the new submariner engine pod won't be
	// scheduled on it.
	By(fmt.Sprintf("Setting the gateway label for node %q to false", initialGatewayNode.Name))
	f.SetGatewayLabelOnNode(framework.ClusterA, initialGatewayNode.Name, false)

	By(fmt.Sprintf("Deleting submariner engine pod %q", enginePod.Name))
	f.DeletePod(framework.ClusterA, enginePod.Name, framework.TestContext.SubmarinerNamespace)

	// Ensure the new engine pod is started before we run the connectivity tests to eliminate possible timing issue where,
	// after deleting the old pod, we actually run the connectivity test against the oold engine instance before k8s has
	// a chance to react to stop the process/container etc.
	newEnginePod := f.AwaitSubmarinerEnginePod(framework.ClusterA)
	Expect(newEnginePod.Spec.NodeName).To(Equal(initialNonGatewayNode.Name),
		"The new engine pod is not running on the expected node")
	By(fmt.Sprintf("Found new submariner engine pod %q", newEnginePod.Name))

	// Verify a new Endpoint instance is created by the new engine instance. This is a bit whitebox but it's a ssanity check
	// and also gives it a bit more of a cushion to avoid premature timeout in the connectivity test.
	newSubmEndpoint := f.AwaitNewSubmarinerEndpoint(framework.ClusterA, submEndpoint.ObjectMeta.UID)
	By(fmt.Sprintf("Found new submariner endpoint for %q: %#v", clusterAName, newSubmEndpoint))

	By(fmt.Sprintf("Verifying TCP connectivity from gateway node on %q to gateway node on %q", clusterBName, clusterAName))
	tcp.RunConnectivityTest(f, false, framework.PodNetworking, framework.GatewayNode, framework.GatewayNode, framework.ClusterA, framework.ClusterB)

	By(fmt.Sprintf("Verifying TCP connectivity from non-gateway node on %q to non-gateway node on %q", clusterBName, clusterAName))
	tcp.RunConnectivityTest(f, false, framework.PodNetworking, framework.NonGatewayNode, framework.NonGatewayNode, framework.ClusterA, framework.ClusterB)
}

func testTwoGatewayNodesWithTwoReplicas(f *framework.Framework, deployment *appsv1.Deployment) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]
	clusterBName := framework.TestContext.ClusterIDs[framework.ClusterB]

	gatewayNodes := f.FindNodesByGatewayLabel(framework.ClusterA, true)
	Expect(gatewayNodes).To(HaveLen(1), fmt.Sprintf("Expected only one gateway node on %q", clusterAName))
	initGatewayNode := gatewayNodes[0]
	By(fmt.Sprintf("Found gateway node %q on %q", initGatewayNode.Name, clusterAName))

	nonGatewayNodes := f.FindNodesByGatewayLabel(framework.ClusterA, false)
	Expect(nonGatewayNodes).ToNot(BeZero(), fmt.Sprintf("Expected at least one non-gateway node on %q", clusterAName))
	nonGatewayNode := nonGatewayNodes[0]
	By(fmt.Sprintf("Found non-gateway node %q on %q", nonGatewayNode.Name, clusterAName))

	firstEnginePod := f.AwaitSubmarinerEnginePod(framework.ClusterA)
	By(fmt.Sprintf("Found active submariner engine pod %q on %q", firstEnginePod.Name, clusterAName))

	submEndpoint := f.AwaitSubmarinerEndpoint(framework.ClusterA, framework.NoopCheckEndpoint)
	By(fmt.Sprintf("Found submariner endpoint for %q: %#v", clusterAName, submEndpoint))

	By(fmt.Sprintf("Setting the replicas to 2 for submariner engine deployment %q on %q", deployment.Name, clusterAName))
	f.SetReplicas(framework.ClusterA, deployment, 2)

	By(fmt.Sprintf("Setting the gateway label for node %q to true", nonGatewayNode.Name))
	f.SetGatewayLabelOnNode(framework.ClusterA, nonGatewayNode.Name, true)

	By(fmt.Sprintf("Awaiting second submariner engine pod running on %q...", clusterAName))
	enginePods := f.AwaitPodsByAppLabel(framework.ClusterA, framework.SubmarinerEngine, framework.TestContext.SubmarinerNamespace, 2)
	By(fmt.Sprintf("2 submariner engine pods now running on %q: %q and %q", clusterAName, enginePods.Items[0].Name, enginePods.Items[1].Name))

	By(fmt.Sprintf("Setting the gateway label for node %q to false", initGatewayNode.Name))
	f.SetGatewayLabelOnNode(framework.ClusterA, initGatewayNode.Name, false)

	By(fmt.Sprintf("Deleting active submariner engine pod %q", firstEnginePod.Name))
	f.DeletePod(framework.ClusterA, firstEnginePod.Name, framework.TestContext.SubmarinerNamespace)

	By(fmt.Sprintf("Awaiting new submariner endpoint on %q...", clusterAName))
	newSubmEndpoint := f.AwaitNewSubmarinerEndpoint(framework.ClusterA, submEndpoint.ObjectMeta.UID)
	By(fmt.Sprintf("Found new submariner endpoint for %q: %#v", clusterAName, newSubmEndpoint))

	By(fmt.Sprintf("Verifying TCP connectivity from gateway node on %q to gateway node on %q", clusterBName, clusterAName))
	tcp.RunConnectivityTest(f, false, framework.PodNetworking, framework.GatewayNode, framework.GatewayNode, framework.ClusterA, framework.ClusterB)

	By(fmt.Sprintf("Verifying TCP connectivity from non-gateway node on %q to non-gateway node on %q", clusterBName, clusterAName))
	tcp.RunConnectivityTest(f, false, framework.PodNetworking, framework.NonGatewayNode, framework.NonGatewayNode, framework.ClusterA, framework.ClusterB)
}
