package dataplane

import (
	"strings"

	"github.com/submariner-io/submariner/test/e2e/framework"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("[dataplane] Basic Pod to Service tests across clusters without discovery", func() {

	f := framework.NewDefaultFramework("dataplane-p2s-nd")

	It("Should be able to perform a Pod to Service TCP connection and exchange data between different clusters NonGW to NonGW", func() {
		TestPod2ServiceTCP(f, framework.NonGatewayNode, framework.NonGatewayNode, framework.ClusterB, framework.ClusterA)
	})

	It("Should be able to perform a Pod to Service TCP connection and exchange data between different clusters GW to GW", func() {
		TestPod2ServiceTCP(f, framework.GatewayNode, framework.GatewayNode, framework.ClusterB, framework.ClusterA)
	})

	It("Should preserve the source IP (GW to GW node)", func() {
		testPod2ServiceTCPIPPreservation(f, framework.GatewayNode, framework.GatewayNode)
	})

	It("Should preserve the source IP (NonGW to NonGW node)", func() {
		testPod2ServiceTCPIPPreservation(f, framework.NonGatewayNode, framework.NonGatewayNode)
	})
})

func TestPod2ServiceTCP(f *framework.Framework, leftScheduling framework.NetworkPodScheduling, rightScheduling framework.NetworkPodScheduling, listenerPodCluster framework.ClusterIndex, connectorPodCluster framework.ClusterIndex) {

	listenerPod, connectorPod := runAndVerifyNetworkPod2ServicePair(f, leftScheduling, rightScheduling, listenerPodCluster, connectorPodCluster)

	By("Verifying what the pods sent to each other contain the right UUIDs")
	Expect(listenerPod.TerminationMessage).To(ContainSubstring(connectorPod.Config.Data))
	Expect(connectorPod.TerminationMessage).To(ContainSubstring(listenerPod.Config.Data))
}

func testPod2ServiceTCPIPPreservation(f *framework.Framework, leftScheduling framework.NetworkPodScheduling, rightScheduling framework.NetworkPodScheduling) {
	listenerPod, connectorPod := runAndVerifyNetworkPod2ServicePair(f, rightScheduling, leftScheduling, framework.ClusterB, framework.ClusterA)
	framework.Logf("Connector pod has IP: %s", connectorPod.Pod.Status.PodIP)
	By("Verifying the output of listener pod which must contain the source IP")
	Expect(listenerPod.TerminationMessage).To(ContainSubstring(connectorPod.Pod.Status.PodIP))
}

func runAndVerifyNetworkPod2ServicePair(f *framework.Framework, leftScheduling framework.NetworkPodScheduling, rightScheduling framework.NetworkPodScheduling, listenerPodCluster framework.ClusterIndex, connectorPodCluster framework.ClusterIndex) (*framework.NetworkPod, *framework.NetworkPod) {
	By("Creating a listener pod in cluster B, which will wait for a handshake over TCP")
	listenerPod := f.NewNetworkPod(&framework.NetworkPodConfig{
		Type:       framework.ListenerPod,
		Cluster:    listenerPodCluster,
		Scheduling: rightScheduling,
	})

	By("Pointing a service ClusterIP to the listener pod in cluster B")
	service := listenerPod.CreateService()
	framework.Logf("Service for listener pod has ClusterIP: %v", service.Spec.ClusterIP)

	By("Creating a connector pod in cluster A, which will attempt the specific UUID handshake over TCP")
	connectorPod := f.NewNetworkPod(&framework.NetworkPodConfig{
		Type:       framework.ConnectorPod,
		Cluster:    connectorPodCluster,
		Scheduling: leftScheduling,
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
