package dataplane

import (
	"fmt"

	"github.com/submariner-io/submariner/test/e2e/framework"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("[dataplane] Basic TCP connectivity tests across clusters without discovery", func() {
	f := framework.NewDefaultFramework("dataplane-conn-nd")
	var useService bool

	verifyInteraction := func(listenerScheduling, connectorScheduling framework.NetworkPodScheduling) {
		It("should have sent the expected data from the pod to the other pod", func() {
			RunConnectivityTest(f, useService, listenerScheduling, connectorScheduling, framework.ClusterB, framework.ClusterA)
		})
	}

	When("a pod connects via TCP to a remote pod", func() {
		BeforeEach(func() {
			useService = false
		})

		When("the pod is not on a gateway and the remote pod is not on a gateway", func() {
			verifyInteraction(framework.NonGatewayNode, framework.NonGatewayNode)
		})

		When("the pod is not on a gateway and the remote pod is on a gateway", func() {
			verifyInteraction(framework.GatewayNode, framework.NonGatewayNode)
		})

		When("the pod is on a gateway and the remote pod is not on a gateway", func() {
			verifyInteraction(framework.NonGatewayNode, framework.GatewayNode)
		})

		When("the pod is on a gateway and the remote pod is on a gateway", func() {
			verifyInteraction(framework.GatewayNode, framework.GatewayNode)
		})
	})

	When("a pod connects via TCP to a remote service", func() {
		BeforeEach(func() {
			useService = true
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

func RunConnectivityTest(f *framework.Framework, useService bool, listenerScheduling framework.NetworkPodScheduling, connectorScheduling framework.NetworkPodScheduling, listenerCluster framework.ClusterIndex, connectorCluster framework.ClusterIndex) (*framework.NetworkPod, *framework.NetworkPod) {
	By(fmt.Sprintf("Creating a listener pod in cluster %q, which will wait for a handshake over TCP", framework.TestContext.KubeContexts[listenerCluster]))
	listenerPod := f.NewNetworkPod(&framework.NetworkPodConfig{
		Type:       framework.ListenerPod,
		Cluster:    listenerCluster,
		Scheduling: listenerScheduling,
	})

	remoteIP := listenerPod.Pod.Status.PodIP
	if useService {
		By(fmt.Sprintf("Pointing a service ClusterIP to the listener pod in cluster %q", framework.TestContext.KubeContexts[listenerCluster]))
		service := listenerPod.CreateService()
		remoteIP = service.Spec.ClusterIP
	}

	framework.Logf("Will send traffic to IP: %v", remoteIP)

	By(fmt.Sprintf("Creating a connector pod in cluster %q, which will attempt the specific UUID handshake over TCP", framework.TestContext.KubeContexts[connectorCluster]))
	connectorPod := f.NewNetworkPod(&framework.NetworkPodConfig{
		Type:       framework.ConnectorPod,
		Cluster:    connectorCluster,
		Scheduling: connectorScheduling,
		RemoteIP:   remoteIP,
	})

	By(fmt.Sprintf("Waiting for the listener pod %q to exit with code 0, returning what listener sent", listenerPod.Pod.Name))
	listenerPod.AwaitFinish()

	By(fmt.Sprintf("Waiting for the connector pod %q to exit with code 0, returning what connector sent", connectorPod.Pod.Name))
	connectorPod.AwaitFinish()

	listenerPod.CheckSuccessfulFinish()
	connectorPod.CheckSuccessfulFinish()

	By("Verifying that the listener got the connector's data and the connector got the listener's data")
	Expect(listenerPod.TerminationMessage).To(ContainSubstring(connectorPod.Config.Data))
	Expect(connectorPod.TerminationMessage).To(ContainSubstring(listenerPod.Config.Data))

	framework.Logf("Connector pod has IP: %s", connectorPod.Pod.Status.PodIP)
	By("Verifying the output of listener pod which must contain the source IP")
	Expect(listenerPod.TerminationMessage).To(ContainSubstring(connectorPod.Pod.Status.PodIP))

	// Return the pods in case further verification is needed
	return listenerPod, connectorPod
}
