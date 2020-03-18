package dataplane

import (
	. "github.com/onsi/ginkgo"
	"github.com/submariner-io/submariner/test/e2e/framework"
	"github.com/submariner-io/submariner/test/e2e/tcp"
)

var _ = Describe("[dataplane-globalnet] Basic TCP connectivity tests across overlapping clusters without discovery", func() {
	f := framework.NewDefaultFramework("dataplane-gn-conn-nd")
	var listenerEndpoint framework.RemoteEndpoint
	var networkType bool

	verifyInteraction := func(listenerScheduling, connectorScheduling framework.NetworkPodScheduling) {
		It("should have sent the expected data from the pod to the other pod", func() {
			if !framework.TestContext.GlobalnetEnabled {
				framework.Skipf("Globalnet is not enabled, skipping the test...")
				return
			}
			tcp.RunConnectivityTest(f, listenerEndpoint, networkType, listenerScheduling, connectorScheduling, framework.ClusterB, framework.ClusterA)
		})
	}

	When("a pod connects via TCP to a remote service", func() {
		BeforeEach(func() {
			listenerEndpoint = framework.GlobalIP
			networkType = framework.PodNetworking
		})

		When("the pod is not on a gateway and the remote service is not on a gateway", func() {
			verifyInteraction(framework.NonGatewayNode, framework.NonGatewayNode)
		})

		When("the pod is not on a gateway and the remote service is on a gateway", func() {
			verifyInteraction(framework.GatewayNode, framework.NonGatewayNode)
		})

		When("the pod is on a gateway and the remote service is not on a gateway", func() {
			verifyInteraction(framework.NonGatewayNode, framework.GatewayNode)
		})

		When("the pod is on a gateway and the remote service is on a gateway", func() {
			verifyInteraction(framework.GatewayNode, framework.GatewayNode)
		})
	})
})
