package cluster

import (
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/submariner-io/submariner/test/e2e/dataplane"
	"github.com/submariner-io/submariner/test/e2e/framework"
)

var _ = Describe("Test to check adding/removing a new cluster to existing setup", func() {
	f := framework.NewDefaultFramework("add-remove-cluster")

	It("Should be able to add and remove 3rd cluster", func() {
		By(fmt.Sprintf("Verifying that initially pods from the other clusters can not communicate to service/pod in cluster 1"))
		dataplane.RunNoConnectivityTest(f, false, framework.NonGatewayNode, framework.NonGatewayNode, framework.ClusterC, framework.ClusterA)
		//dataplane.RunNoConnectivityTest(f, true, framework.NonGatewayNode, framework.GatewayNode, framework.ClusterC, framework.ClusterB)

		By(fmt.Sprintf("Adding a new cluster to the setup by setting gateway label to one of the non gateway nodes on cluster 1"))
		nonGatewayNode := f.FindNodesByGatewayLabel(framework.ClusterC, false)[0].Name
		f.SetGatewayLabelOnNode(framework.ClusterC, nonGatewayNode, true)
		gatewayNodes := f.FindNodesByGatewayLabel(framework.ClusterC, true)
		framework.Logf("gateway node is %q", gatewayNodes[0].Name)
		Expect(gatewayNodes).To(HaveLen(1), fmt.Sprintf("Expected only one gateway node on %q", framework.ClusterC))

		enginePod := f.AwaitSubmarinerEnginePod(framework.ClusterC)
		By(fmt.Sprintf("Found submariner engine pod %q on Cluster 1", enginePod.Name))

		By(fmt.Sprintf("Verifying that pods/services from the other clusters can communicate to service in cluster 1"))
		//dataplane.RunConnectivityTest(f, false, framework.GatewayNode, framework.GatewayNode, framework.ClusterC, framework.ClusterA)
		//dataplane.RunConnectivityTest(f, true, framework.GatewayNode, framework.GatewayNode, framework.ClusterC, framework.ClusterB)

		By(fmt.Sprintf("Removing cluster 1 from existing setup by unseting gateway label on the gateway node and deleting SubM pod"))
		f.SetGatewayLabelOnNode(framework.ClusterC, nonGatewayNode, false)
		removedgatewayNodes := f.FindNodesByGatewayLabel(framework.ClusterC, true)
		Expect(removedgatewayNodes).To(HaveLen(0), fmt.Sprintf("Expected no gateway node on %q", framework.ClusterC))
		f.DeletePod(framework.ClusterC, enginePod.Name, "submariner")

		By(fmt.Sprintf("Verifying that pods from the other clusters can not communicate to service in cluster C"))
		dataplane.RunNoConnectivityTest(f, true, framework.NonGatewayNode, framework.NonGatewayNode, framework.ClusterC, framework.ClusterA)
		//dataplane.RunNoConnectivityTest(f, false, framework.NonGatewayNode, framework.GatewayNode, framework.ClusterC, framework.ClusterB)

	})
})
