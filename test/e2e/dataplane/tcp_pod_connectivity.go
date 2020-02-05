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

	listenerPod, connectorPod := createPods(f, useService, listenerScheduling, connectorScheduling, listenerCluster, connectorCluster,
		framework.TestContext.ConnectionTimeout, framework.TestContext.ConnectionAttempts)
	listenerPod.CheckSuccessfulFinish()
	connectorPod.CheckSuccessfulFinish()

	By("Verifying that the listener got the connector's data and the connector got the listener's data")
	Expect(listenerPod.TerminationMessage).To(ContainSubstring(connectorPod.Config.Data))
	Expect(connectorPod.TerminationMessage).To(ContainSubstring(listenerPod.Config.Data))

	By("Verifying the output of listener pod which must contain the source IP")
	Expect(listenerPod.TerminationMessage).To(ContainSubstring(connectorPod.Pod.Status.PodIP))

	// Return the pods in case further verification is needed
	return listenerPod, connectorPod
}

func RunNoConnectivityTest(f *framework.Framework, useService bool, listenerScheduling framework.NetworkPodScheduling, connectorScheduling framework.NetworkPodScheduling, listenerCluster framework.ClusterIndex, connectorCluster framework.ClusterIndex) (*framework.NetworkPod, *framework.NetworkPod) {
	listenerPod, connectorPod := createPods(f, useService, listenerScheduling, connectorScheduling, listenerCluster, connectorCluster, 5, 1)

	By("Verifying that listener pod exits with non-zero code and timed out message")
	Expect(listenerPod.TerminationMessage).To(ContainSubstring("nc: timeout"))
	Expect(listenerPod.TerminationCode).To(Equal(int32(1)))

	By("Verifying that connector pod exists with zero code but times out")
	Expect(connectorPod.TerminationMessage).To(ContainSubstring("Connection timed out"))
	Expect(connectorPod.TerminationCode).To(Equal(int32(0)))

	// Return the pods in case further verification is needed
	return listenerPod, connectorPod
}

func createPods(f *framework.Framework, useService bool, listenerScheduling framework.NetworkPodScheduling, connectorScheduling framework.NetworkPodScheduling, listenerCluster framework.ClusterIndex,
	connectorCluster framework.ClusterIndex, connectionTimeout uint, connectionAttempts uint) (*framework.NetworkPod, *framework.NetworkPod) {

	By(fmt.Sprintf("Creating a listener pod in cluster %q, which will wait for a handshake over TCP", framework.TestContext.ClusterIDs[listenerCluster]))
	listenerPod := f.NewNetworkPod(&framework.NetworkPodConfig{
		Type:               framework.ListenerPod,
		Cluster:            listenerCluster,
		Scheduling:         listenerScheduling,
		ConnectionTimeout:  connectionTimeout,
		ConnectionAttempts: connectionAttempts,
	})

	remoteIP := listenerPod.Pod.Status.PodIP
	if useService {
		By(fmt.Sprintf("Pointing a service ClusterIP to the listener pod in cluster %q", framework.TestContext.ClusterIDs[listenerCluster]))
		service := listenerPod.CreateService()
		remoteIP = service.Spec.ClusterIP
	}

	framework.Logf("Will send traffic to IP: %v", remoteIP)

	By(fmt.Sprintf("Creating a connector pod in cluster %q, which will attempt the specific UUID handshake over TCP", framework.TestContext.ClusterIDs[connectorCluster]))
	connectorPod := f.NewNetworkPod(&framework.NetworkPodConfig{
		Type:               framework.ConnectorPod,
		Cluster:            connectorCluster,
		Scheduling:         connectorScheduling,
		RemoteIP:           remoteIP,
		ConnectionTimeout:  connectionTimeout,
		ConnectionAttempts: connectionAttempts,
	})

	By(fmt.Sprintf("Waiting for the listener pod %q to exit, returning what listener sent", listenerPod.Pod.Name))
	listenerPod.AwaitFinish()

	By(fmt.Sprintf("Waiting for the connector pod %q to exit, returning what connector sent", connectorPod.Pod.Name))
	connectorPod.AwaitFinish()

	framework.Logf("Connector pod has IP: %s", connectorPod.Pod.Status.PodIP)

	return listenerPod, connectorPod
}
