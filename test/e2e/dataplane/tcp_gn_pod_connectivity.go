package dataplane

import (
	. "github.com/onsi/ginkgo"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/tcp"
)

var _ = Describe("[dataplane-globalnet] Basic TCP connectivity tests across overlapping clusters without discovery", func() {
	f := framework.NewFramework("dataplane-gn-conn-nd")
	var toEndpointType tcp.EndpointType
	var networking framework.NetworkingType

	verifyInteraction := func(fromClusterScheduling, toClusterScheduling framework.NetworkPodScheduling) {
		It("should have sent the expected data from the pod to the other pod", func() {
			if !framework.TestContext.GlobalnetEnabled {
				framework.Skipf("Globalnet is not enabled, skipping the test...")
				return
			}

			tcp.RunConnectivityTest(tcp.ConnectivityTestParams{
				Framework:             f,
				ToEndpointType:        toEndpointType,
				Networking:            networking,
				FromCluster:           framework.ClusterA,
				FromClusterScheduling: fromClusterScheduling,
				ToCluster:             framework.ClusterB,
				ToClusterScheduling:   toClusterScheduling,
			})
		})
	}

	When("a pod connects via TCP to a remote service globalIP", func() {
		BeforeEach(func() {
			toEndpointType = tcp.GlobalIP
			networking = framework.PodNetworking
		})

		When("the pod is not on a gateway and the remote service is not on a gateway", func() {
			verifyInteraction(framework.NonGatewayNode, framework.NonGatewayNode)
		})

		When("the pod is not on a gateway and the remote service is on a gateway", func() {
			verifyInteraction(framework.NonGatewayNode, framework.GatewayNode)
		})

		When("the pod is on a gateway and the remote service is not on a gateway", func() {
			verifyInteraction(framework.GatewayNode, framework.NonGatewayNode)
		})

		When("the pod is on a gateway and the remote service is on a gateway", func() {
			verifyInteraction(framework.GatewayNode, framework.GatewayNode)
		})
	})

	When("a pod with HostNetworking connects via TCP to a remote service globalIP", func() {
		BeforeEach(func() {
			toEndpointType = tcp.GlobalIP
			networking = framework.HostNetworking
		})

		When("the pod is not on a gateway and the remote service is not on a gateway", func() {
			verifyInteraction(framework.NonGatewayNode, framework.NonGatewayNode)
		})

		When("the pod is on a gateway and the remote service is not on a gateway", func() {
			verifyInteraction(framework.GatewayNode, framework.NonGatewayNode)
		})
	})
})
