package dataplane

import (
	"strings"

	"github.com/submariner-io/submariner/test/e2e/framework"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("[dataplane] Basic Pod to Service tests across clusters without discovery", func() {
	f := framework.NewDefaultFramework("dataplane-p2s-nd")

	verifyInteraction := func(listenerScheduling, connectorScheduling framework.NetworkPodScheduling) {
		It("should have sent the expected data from the pod to the other pod", func() {
			listenerPod, connectorPod := runAndVerifyNetworkPod2ServicePair(f, listenerScheduling, connectorScheduling, framework.ClusterB, framework.ClusterA)
			Expect(listenerPod.TerminationMessage).To(ContainSubstring(connectorPod.Config.Data))
			Expect(connectorPod.TerminationMessage).To(ContainSubstring(listenerPod.Config.Data))

			framework.Logf("Connector pod has IP: %s", connectorPod.Pod.Status.PodIP)
			By("Verifying the output of listener pod which must contain the source IP")
			Expect(listenerPod.TerminationMessage).To(ContainSubstring(connectorPod.Pod.Status.PodIP))
		})
	}

	When("a pod connects via TCP to a service and sends it data", func() {
		When("the pod is not on the gateway and the service (pod) is not on the gateway", func() {
			verifyInteraction(framework.NonGatewayNode, framework.NonGatewayNode)
		})

		When("the pod is on the gateway and the service (pod) is on the gateway", func() {
			verifyInteraction(framework.GatewayNode, framework.GatewayNode)
		})
	})
})

func runAndVerifyNetworkPod2ServicePair(f *framework.Framework, listenerScheduling framework.NetworkPodScheduling, connectorScheduling framework.NetworkPodScheduling, listenerCluster framework.ClusterIndex, connectorCluster framework.ClusterIndex) (*framework.NetworkPod, *framework.NetworkPod) {
	By("Creating a listener pod in cluster B, which will wait for a handshake over TCP")
	listenerPod := f.NewNetworkPod(&framework.NetworkPodConfig{
		Type:       framework.ListenerPod,
		Cluster:    listenerCluster,
		Scheduling: listenerScheduling,
	})

	By("Pointing a service ClusterIP to the listener pod in cluster B")
	service := listenerPod.CreateService()
	framework.Logf("Service for listener pod has ClusterIP: %v", service.Spec.ClusterIP)

	By("Creating a connector pod in cluster A, which will attempt the specific UUID handshake over TCP")
	connectorPod := f.NewNetworkPod(&framework.NetworkPodConfig{
		Type:       framework.ConnectorPod,
		Cluster:    connectorCluster,
		Scheduling: connectorScheduling,
		RemoteIP:   service.Spec.ClusterIP,
	})

	By("Waiting for the listener pod to exit with code 0, returning what listener sent")
	listenerPod.AwaitSuccessfulFinish()
	framework.Logf("Listener output:\n%s", keepLines(listenerPod.TerminationMessage, 3))

	By("Waiting for the connector pod to exit with code 0, returning what connector sent")
	connectorPod.AwaitSuccessfulFinish()
	framework.Logf("Connector output\n%s", keepLines(connectorPod.TerminationMessage, 2))

	return listenerPod, connectorPod
}

func keepLines(output string, n int) string {
	lines := strings.Split(output, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
