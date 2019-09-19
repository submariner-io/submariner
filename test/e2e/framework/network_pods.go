package framework

import (
	"strconv"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/gomega"
)

const (
	TestPort = 1234
)

// create a test pod inside the current test namespace on the specified cluster.
// The pod will listen on TestPort over TCP, send sendString over the connection,
// and write the network response in the pod  termination log, then exit with 0 status
func (f *Framework) CreateTCPCheckListenerPod(cluster ClusterIndex, scheduling TestPodScheduling, sendString string) *v1.Pod {

	tcpCheckListenerPod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "tcp-check-listener",
			Labels: map[string]string{
				TestAppLabel: "tcp-check-listener",
			},
		},
		Spec: v1.PodSpec{
			Affinity:      nodeAffinity(scheduling),
			RestartPolicy: v1.RestartPolicyNever,
			Containers: []v1.Container{
				{
					Name:  "tcp-check-listener",
					Image: "busybox",
					// We send the string 50 times to put more pressure on the TCP connection and avoid limited
					// resource environments from not sending at least some data before timeout.
					Command: []string{"sh", "-c", "for i in $(seq 50); do echo listener says $SEND_STRING; done | nc -l -v -p $LISTEN_PORT -s 0.0.0.0 >/dev/termination-log 2>&1"},
					Env: []v1.EnvVar{
						{Name: "LISTEN_PORT", Value: strconv.Itoa(TestPort)},
						{Name: "SEND_STRING", Value: sendString},
					},
				},
			},
		},
	}

	pc := f.ClusterClients[cluster].CoreV1().Pods(f.Namespace)
	tcpListenerPod, err := pc.Create(&tcpCheckListenerPod)
	Expect(err).NotTo(HaveOccurred())
	return f.WaitForPodToBeReady(tcpListenerPod, cluster)
}

// create a test pod inside the current test namespace on the specified cluster.
// The pod will connect to remoteIP:TestPort over TCP, send sendString over the
// connection, and write the network response in the pod termination log, then
// exit with 0 status
func (f *Framework) CreateTCPCheckConnectorPod(cluster ClusterIndex, scheduling TestPodScheduling, remoteIP string, sendString string) *v1.Pod {

	tcpCheckConnectorPod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "tcp-check-pod",
			Labels: map[string]string{
				TestAppLabel: "tcp-check-pod",
			},
		},
		Spec: v1.PodSpec{
			Affinity:      nodeAffinity(scheduling),
			RestartPolicy: v1.RestartPolicyNever,
			Containers: []v1.Container{
				{
					Name:  "tcp-check-connector",
					Image: "busybox",
					// We send the string 50 times to put more pressure on the TCP connection and avoid limited
					// resource environments from not sending at least some data before timeout.
					Command: []string{"sh", "-c", "for in in $(seq 50); do echo connector says $SEND_STRING; done | nc -v $REMOTE_IP $REMOTE_PORT -w 8 >/dev/termination-log 2>&1"},
					Env: []v1.EnvVar{
						{Name: "REMOTE_PORT", Value: strconv.Itoa(TestPort)},
						{Name: "SEND_STRING", Value: sendString},
						{Name: "REMOTE_IP", Value: remoteIP},
					},
				},
			},
		},
	}

	pc := f.ClusterClients[cluster].CoreV1().Pods(f.Namespace)
	tcpCheckPod, err := pc.Create(&tcpCheckConnectorPod)
	Expect(err).NotTo(HaveOccurred())
	return tcpCheckPod
}

type TestPodScheduling int

const (
	InvalidScheduling TestPodScheduling = iota
	GatewayNode
	NonGatewayNode
)

func nodeAffinity(scheduling TestPodScheduling) *v1.Affinity {

	const gatewayLabel = "submariner.io/gateway"

	var nodeSelReqs []v1.NodeSelectorRequirement

	switch scheduling {
	case GatewayNode:
		nodeSelReqs = addNodeSelectorRequirement(nodeSelReqs, gatewayLabel, v1.NodeSelectorOpIn, []string{"true"})

	case NonGatewayNode:
		nodeSelReqs = addNodeSelectorRequirement(nodeSelReqs, gatewayLabel, v1.NodeSelectorOpDoesNotExist, nil)
		nodeSelReqs = addNodeSelectorRequirement(nodeSelReqs, gatewayLabel, v1.NodeSelectorOpNotIn, []string{"true"})
	}

	affinity := v1.Affinity{
		NodeAffinity: &v1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
				NodeSelectorTerms: []v1.NodeSelectorTerm{
					{
						MatchExpressions: nodeSelReqs,
					},
				},
			},
		},
	}
	return &affinity
}

func addNodeSelectorRequirement(nodeSelReqs []v1.NodeSelectorRequirement, label string,
	op v1.NodeSelectorOperator, values []string) []v1.NodeSelectorRequirement {
	return append(nodeSelReqs, v1.NodeSelectorRequirement{
		Key:      label,
		Operator: v1.NodeSelectorOpNotIn,
		Values:   []string{"true"},
	})
}
