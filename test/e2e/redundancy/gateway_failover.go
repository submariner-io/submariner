package redundancy

import (
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/test/e2e/dataplane"
	"github.com/submariner-io/submariner/test/e2e/framework"
)

var _ = Describe("[redundancy] Gateway fail-over tests", func() {
	f := framework.NewDefaultFramework("gateway-redundancy")

	When("one gateway node is configured and the submariner engine pod fails", func() {
		It("should start a new submariner engine pod and be able to connect from another cluster", func() {
			testOneGatewayNode(f)
		})
	})
})

func testOneGatewayNode(f *framework.Framework) {
	clusterAName := framework.TestContext.KubeContexts[framework.ClusterA]
	clusterBName := framework.TestContext.KubeContexts[framework.ClusterB]

	By(fmt.Sprintf("Sanity check - ensuring there's only one gateway node on %q", clusterAName))
	gatewayNodes := f.FindNodesByGatewayLabel(framework.ClusterA, true)
	Expect(gatewayNodes).To(HaveLen(1), fmt.Sprintf("Expected only one gateway node on %q", clusterAName))

	enginePod := f.AwaitSubmarinerEnginePod(framework.ClusterA)
	By(fmt.Sprintf("Found submariner engine pod %q on %q", enginePod.Name, clusterAName))

	By(fmt.Sprintf("Deleting submariner engine pod %q", enginePod.Name))
	f.DeletePod(framework.ClusterA, enginePod.Name, framework.TestContext.SubmarinerNamespace)

	newEnginePod := f.AwaitSubmarinerEnginePod(framework.ClusterA)
	By(fmt.Sprintf("Found new submariner engine pod %q", newEnginePod.Name))

	By(fmt.Sprintf("Verifying TCP connectivity from %q to %q", clusterBName, clusterAName))
	dataplane.RunConnectivityTest(f, false, framework.GatewayNode, framework.GatewayNode, framework.ClusterA, framework.ClusterB)
	dataplane.RunConnectivityTest(f, false, framework.NonGatewayNode, framework.NonGatewayNode, framework.ClusterA, framework.ClusterB)
}
