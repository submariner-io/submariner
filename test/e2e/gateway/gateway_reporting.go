package redundancy

import (
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	v1meta "k8s.io/apimachinery/pkg/apis/meta/v1"

	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	subFramework "github.com/submariner-io/submariner/test/e2e/framework"
)

var _ = Describe("[gateway] Gateway reporting tests", func() {
	f := subFramework.NewFramework("gateway-reporting")
	AfterEach(f.GatewayCleanup)

	When("any gateway node is configured", func() {
		It("should be reported to the Gateway API", func() {
			testBasicGatewayReporting(f)
		})
	})

	When("multiple gateway nodes are configured", func() {
		It("only one is active", func() {
			testOnlyOneActiveGateway(f)
		})
	})

})

func testBasicGatewayReporting(f *subFramework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]

	By(fmt.Sprintf("Sanity check - ensuring there's exactly one gateway node on %q", clusterAName))
	gatewayNodes := f.FindNodesByGatewayLabel(framework.ClusterA, true)
	Expect(gatewayNodes).To(HaveLen(1), fmt.Sprintf("Expected only one gateway node on %q", clusterAName))

	By(fmt.Sprintf("Ensuring that the gateway reports as active %q", clusterAName))
	gatewayClient := subFramework.SubmarinerClients[framework.ClusterA].SubmarinerV1().Gateways(
		framework.TestContext.SubmarinerNamespace)
	gwList, err := gatewayClient.List(v1meta.ListOptions{LabelSelector: subv1.HAStatusActiveSelector})
	Expect(err).NotTo(HaveOccurred())
	Expect(gwList.Items).To(HaveLen(1))

	gw := gwList.Items[0]

	By(fmt.Sprintf("Ensuring that the gateway %q is reporting connections", gw.Name))

	Expect(gw.Status.Connections).NotTo(BeEmpty())
	Expect(gw.Status.Connections[0].Status).To(Equal(subv1.Connected))
}

func testOnlyOneActiveGateway(f *subFramework.Framework) {
	clusterAName := framework.TestContext.ClusterIDs[framework.ClusterA]

	gatewayNodes := f.FindNodesByGatewayLabel(framework.ClusterA, true)
	Expect(gatewayNodes).To(HaveLen(1), fmt.Sprintf("Expected only one gateway node on %q", clusterAName))

	initialGatewayNode := gatewayNodes[0]
	By(fmt.Sprintf("Found gateway node %q on %q", initialGatewayNode.Name, clusterAName))

	nonGatewayNodes := f.FindNodesByGatewayLabel(framework.ClusterA, false)
	Expect(len(nonGatewayNodes) > 0).To(BeTrue(), fmt.Sprintf("Expected at least one non-gateway node on %q", clusterAName))

	initialNonGatewayNode := nonGatewayNodes[0]
	By(fmt.Sprintf("Found non-gateway node %q on %q", initialNonGatewayNode.Name, clusterAName))

	f.SetGatewayLabelOnNode(framework.ClusterA, initialNonGatewayNode.Name, true)

	By(fmt.Sprintf("Ensuring that two Gateways become available in cluster %q", clusterAName))

	gwActive := f.AwaitForGatewayWithStatus(framework.ClusterA, initialGatewayNode.Name, subv1.HAStatusActive)
	gwPassive := f.AwaitForGatewayWithStatus(framework.ClusterA, initialNonGatewayNode.Name, subv1.HAStatusPassive)
	Expect(gwActive.Status.Connections).ToNot(BeEmpty(), "The active gateway must have active connections")
	Expect(gwPassive.Status.Connections).To(BeEmpty(), "The passive gateway must have no connections")

	//WIP: We need to put this as a cleanup capture under any error
	f.SetGatewayLabelOnNode(framework.ClusterA, initialNonGatewayNode.Name, false)

}
