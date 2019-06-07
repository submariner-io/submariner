package dataplane

import (
	"k8s.io/apimachinery/pkg/util/uuid"

	"github.com/rancher/submariner/test/e2e/framework"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("[dataplane] Basic Pod to Service tests across clusters without discovery", func() {
	f := framework.NewDefaultFramework("dataplane-p2s-nd")
	It("Should be able to perform a Pod to Service TCP connection and exchange data between different clusters", func() {
		testPod2ServiceTCP(f)
	})
})

func testPod2ServiceTCP(f *framework.Framework) {

	listenerUUID := string(uuid.NewUUID())
	connectorUUID := string(uuid.NewUUID())

	By("Creating a listener pod in cluster B, which will wait for a handshake over TCP")
	listenerPod := f.CreateTCPCheckListenerPod(framework.ClusterB, listenerUUID)

	By("Pointing a service ClusterIP to the listerner pod in cluster B")
	service := f.CreateTCPService(framework.ClusterB, listenerPod.Labels[framework.TestAppLabel], framework.TestPort)
	framework.Logf("Service for listener pod has ClusterIP: %v", service.Spec.ClusterIP)

	By("Creating a connector pod in cluster B, which will attempt the specific UUID handshake over TCP")
	connectorPod := f.CreateTCPCheckConnectorPod(framework.ClusterA, listenerPod, service.Spec.ClusterIP, connectorUUID)

	By("Waiting for the connector pod to exit with code 0, returning what listener sent")
	exitStatusL, exitMessageL := f.WaitForPodFinishStatus(listenerPod, framework.ClusterB)
	framework.Logf("Listener output:\n%s", exitMessageL)
	Expect(exitStatusL).To(Equal(int32(0)))

	By("Waiting for the listener pod to exit with code 0, returning what connector sent")
	exitStatusC, exitMessageC := f.WaitForPodFinishStatus(connectorPod, framework.ClusterA)
	framework.Logf("Connector output\n%s", exitMessageC)
	Expect(exitStatusC).To(Equal(int32(0)))

	By("Verifying what the pods sent to each other contain the right UUIDs")
	Expect(exitMessageL).To(ContainSubstring(connectorUUID))
	Expect(exitMessageC).To(ContainSubstring(listenerUUID))
}
