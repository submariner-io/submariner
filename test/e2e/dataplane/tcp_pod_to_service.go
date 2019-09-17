package dataplane

import (
	"strings"

	"github.com/submariner-io/submariner/test/e2e/framework"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("[dataplane] Basic Pod to Service tests across clusters without discovery", func() {

	var (
		listenerPod, connectorPod   *v1.Pod
		listenerUUID, connectorUUID string
		exitStatusC, exitStatusL    int32
		exitMessageC, exitMessageL  string
	)

	f := framework.NewDefaultFramework("dataplane-p2s-nd")

	BeforeEach(func() {

		listenerUUID = string(uuid.NewUUID())
		connectorUUID = string(uuid.NewUUID())

		By("Creating a listener pod in cluster B, which will wait for a handshake over TCP")
		listenerPod = f.CreateTCPCheckListenerPod(framework.ClusterB, listenerUUID)

		By("Pointing a service ClusterIP to the listener pod in cluster B")
		service := f.CreateTCPService(framework.ClusterB, listenerPod.Labels[framework.TestAppLabel], framework.TestPort)
		framework.Logf("Service for listener pod has ClusterIP: %v", service.Spec.ClusterIP)

		By("Creating a connector pod in cluster A, which will attempt the specific UUID handshake over TCP")
		connectorPod = f.CreateTCPCheckConnectorPod(framework.ClusterA, listenerPod, service.Spec.ClusterIP, connectorUUID)

		By("Waiting for the listener pod to exit with code 0, returning what listener sent")
		exitStatusL, exitMessageL = f.WaitForPodFinishStatus(listenerPod, framework.ClusterB)
		framework.Logf("Listener output:\n%s", keepLines(exitMessageL, 3))
		Expect(exitStatusL).To(Equal(int32(0)))

		By("Waiting for the connector pod to exit with code 0, returning what connector sent")
		exitStatusC, exitMessageC = f.WaitForPodFinishStatus(connectorPod, framework.ClusterA)
		framework.Logf("Connector output\n%s", keepLines(exitMessageC, 2))
		Expect(exitStatusC).To(Equal(int32(0)))
	})

	It("Should be able to perform a Pod to Service TCP connection and exchange data between different clusters", func() {
		By("Verifying what the pods sent to each other contain the right UUIDs")
		Expect(exitMessageL).To(ContainSubstring(connectorUUID))
		Expect(exitMessageC).To(ContainSubstring(listenerUUID))
	})

	It("Should preserve the source IP", func() {
		By("Retrieving updated connector pod information, including PodIP")
		pc := f.ClusterClients[framework.ClusterA].CoreV1().Pods(f.Namespace)
		connectorPod, err := pc.Get(connectorPod.Name, metav1.GetOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		framework.Logf("Connector pod has IP: %s", connectorPod.Status.PodIP)
		By("Verifying the output of listener pod which must contain the source IP")
		Expect(exitMessageL).To(ContainSubstring(connectorPod.Status.PodIP))
	})

})

func keepLines(output string, n int) string {
	lines := strings.Split(output, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
