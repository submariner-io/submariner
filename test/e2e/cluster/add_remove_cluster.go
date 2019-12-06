package cluster

import (
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/submariner-io/submariner/test/e2e/dataplane"
	"github.com/submariner-io/submariner/test/e2e/framework"
)

var _ = Describe("[expansion] Test expanding/shrinking an existing cluster fleet", func() {
	f := framework.NewDefaultFramework("add-remove-cluster")

	It("Should be able to add and remove third cluster", func() {
		clusterAName := framework.TestContext.KubeContexts[framework.ClusterA]
		clusterBName := framework.TestContext.KubeContexts[framework.ClusterB]
		clusterCName := framework.TestContext.KubeContexts[framework.ClusterC]

		gatewayNode := f.FindNodesByGatewayLabel(framework.ClusterC, true)
		Expect(gatewayNode).To(HaveLen(0), fmt.Sprintf("Expected no gateway node on %q", framework.ClusterC))

		By(fmt.Sprintf("Verifying that a pod in cluster %q cannot connect to a pod in cluster %q", clusterAName, clusterCName))
		dataplane.RunNoConnectivityTest(f, false, framework.NonGatewayNode, framework.NonGatewayNode, framework.ClusterA, framework.ClusterC)

		By(fmt.Sprintf("Verifying that a pod in cluster %q cannot connect to a service in cluster %q", clusterBName, clusterCName))
		dataplane.RunNoConnectivityTest(f, true, framework.NonGatewayNode, framework.NonGatewayNode, framework.ClusterB, framework.ClusterC)

		nonGatewayNode := f.FindNodesByGatewayLabel(framework.ClusterC, false)
		Expect(nonGatewayNode).To(HaveLen(1), fmt.Sprintf("Found non-gateway node %q on %q", nonGatewayNode[0].Name, clusterCName))
		By(fmt.Sprintf("Adding cluster %q by setting the gateway label on node %q", clusterCName, nonGatewayNode))
		f.SetGatewayLabelOnNode(framework.ClusterC, nonGatewayNode[0].Name, true)

		enginePod := f.AwaitSubmarinerEnginePod(framework.ClusterC)
		By(fmt.Sprintf("Found submariner engine pod %q on %q", enginePod.Name, clusterCName))

		By(fmt.Sprintf("Checking connectivity between clusters"))
		dataplane.RunConnectivityTest(f, false, framework.GatewayNode, framework.GatewayNode, framework.ClusterB, framework.ClusterC)
		dataplane.RunConnectivityTest(f, true, framework.GatewayNode, framework.GatewayNode, framework.ClusterA, framework.ClusterC)

		By(fmt.Sprintf("Removing cluster %q by unsetting the gateway label and deleting submariner engine pod %q", clusterCName, enginePod.Name))
		f.SetGatewayLabelOnNode(framework.ClusterC, nonGatewayNode[0].Name, false)
		f.DeletePod(framework.ClusterC, enginePod.Name, framework.TestContext.SubmarinerNamespace)

		By(fmt.Sprintf("Verifying that a pod in cluster %q cannot connect to a pod in cluster %q", clusterAName, clusterCName))
		dataplane.RunNoConnectivityTest(f, true, framework.NonGatewayNode, framework.NonGatewayNode, framework.ClusterA, framework.ClusterC)

		By(fmt.Sprintf("Verifying that a pod in cluster %q cannot connect to a service in cluster %q", clusterBName, clusterCName))
		dataplane.RunNoConnectivityTest(f, false, framework.GatewayNode, framework.NonGatewayNode, framework.ClusterB, framework.ClusterC)
	})
})
