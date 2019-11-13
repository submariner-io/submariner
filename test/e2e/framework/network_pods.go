package framework

import (
	"fmt"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"

	. "github.com/onsi/gomega"
)

type NetworkPodType int

const (
	InvalidPodType NetworkPodType = iota
	ListenerPod
	ConnectorPod
)

type NetworkPodScheduling int

const (
	InvalidScheduling NetworkPodScheduling = iota
	GatewayNode
	NonGatewayNode
)

type NetworkPodConfig struct {
	Type       NetworkPodType
	Cluster    ClusterIndex
	Scheduling NetworkPodScheduling
	Port       int
	Data       string
	RemoteIP   string
	// TODO: namespace, once https://github.com/submariner-io/submariner/pull/141 is merged
}

type NetworkPod struct {
	Pod                 *v1.Pod
	Config              *NetworkPodConfig
	TerminationError    error
	TerminationErrorMsg string
	TerminationCode     int32
	TerminationMessage  string
	ConnectionTimeout   int
	framework           *Framework
}

const (
	TestPort = 1234
)

func (f *Framework) NewNetworkPod(config *NetworkPodConfig) *NetworkPod {

	// check if all necessary details are provided
	Expect(config.Scheduling).ShouldNot(Equal(InvalidScheduling))
	Expect(config.Type).ShouldNot(Equal(InvalidPodType))

	// setup unset defaults
	if config.Port == 0 {
		config.Port = TestPort
	}

	if config.Data == "" {
		config.Data = string(uuid.NewUUID())
	}

	networkPod := &NetworkPod{Config: config, framework: f, TerminationCode: -1, ConnectionTimeout: 45}

	switch config.Type {
	case ListenerPod:
		networkPod.buildTCPCheckListenerPod()
	case ConnectorPod:
		networkPod.buildTCPCheckConnectorPod()
	}

	return networkPod
}

func (np *NetworkPod) AwaitReady() {
	pods := np.framework.ClusterClients[np.Config.Cluster].CoreV1().Pods(np.framework.Namespace)

	np.Pod = AwaitUntil("await pod ready", func() (interface{}, error) {
		return pods.Get(np.Pod.Name, metav1.GetOptions{})
	}, func(result interface{}) (bool, string, error) {
		pod := result.(*v1.Pod)
		if pod.Status.Phase != v1.PodRunning {
			if pod.Status.Phase != v1.PodPending {
				return false, "", fmt.Errorf("unexpected pod phase %v - expected %v or %v", pod.Status.Phase, v1.PodPending, v1.PodRunning)
			}
			return false, fmt.Sprintf("Pod %q is still pending", pod.Name), nil
		}

		return true, "", nil // pod is running
	}).(*v1.Pod)
}

func (np *NetworkPod) AwaitFinish() {
	pods := np.framework.ClusterClients[np.Config.Cluster].CoreV1().Pods(np.framework.Namespace)

	_, np.TerminationErrorMsg, np.TerminationError = AwaitResultOrError(fmt.Sprintf("await pod %q finished", np.Pod.Name), func() (interface{}, error) {
		return pods.Get(np.Pod.Name, metav1.GetOptions{})
	}, func(result interface{}) (bool, string, error) {
		np.Pod = result.(*v1.Pod)

		switch np.Pod.Status.Phase {
		case v1.PodSucceeded:
			return true, "", nil
		case v1.PodFailed:
			return true, "", nil
		default:
			return false, fmt.Sprintf("Pod status is %v", np.Pod.Status.Phase), nil
		}
	})

	finished := np.Pod.Status.Phase == v1.PodSucceeded || np.Pod.Status.Phase == v1.PodFailed
	if finished {
		np.TerminationCode = np.Pod.Status.ContainerStatuses[0].State.Terminated.ExitCode
		np.TerminationMessage = np.Pod.Status.ContainerStatuses[0].State.Terminated.Message

		Logf("Pod %q output:\n%s", np.Pod.Name, removeDupDataplaneLines(np.TerminationMessage))
	}
}

func (np *NetworkPod) CheckSuccessfulFinish() {
	Expect(np.TerminationError).NotTo(HaveOccurred(), np.TerminationErrorMsg)
	Expect(np.TerminationCode).To(Equal(int32(0)))
}

func (np *NetworkPod) CreateService() *v1.Service {
	return np.framework.CreateTCPService(np.Config.Cluster, np.Pod.Labels[TestAppLabel], np.Config.Port)
}

// create a test pod inside the current test namespace on the specified cluster.
// The pod will listen on TestPort over TCP, send sendString over the connection,
// and write the network response in the pod  termination log, then exit with 0 status
func (np *NetworkPod) buildTCPCheckListenerPod() {

	tcpCheckListenerPod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "tcp-check-listener",
			Labels: map[string]string{
				TestAppLabel: "tcp-check-listener",
			},
		},
		Spec: v1.PodSpec{
			Affinity:      nodeAffinity(np.Config.Scheduling),
			RestartPolicy: v1.RestartPolicyNever,
			Containers: []v1.Container{
				{
					Name:  "tcp-check-listener",
					Image: "busybox",
					// We send the string 50 times to put more pressure on the TCP connection and avoid limited
					// resource environments from not sending at least some data before timeout.
					Command: []string{"sh", "-c", "for i in $(seq 50); do echo [dataplane] listener says $SEND_STRING; done | nc -w $CONN_TIMEOUT -l -v -p $LISTEN_PORT -s 0.0.0.0 >/dev/termination-log 2>&1"},
					Env: []v1.EnvVar{
						{Name: "LISTEN_PORT", Value: strconv.Itoa(np.Config.Port)},
						{Name: "SEND_STRING", Value: np.Config.Data},
						{Name: "CONN_TIMEOUT", Value: strconv.Itoa(np.ConnectionTimeout)},
					},
				},
			},
		},
	}

	pc := np.framework.ClusterClients[np.Config.Cluster].CoreV1().Pods(np.framework.Namespace)
	var err error
	np.Pod, err = pc.Create(&tcpCheckListenerPod)
	Expect(err).NotTo(HaveOccurred())
	np.AwaitReady()
}

// create a test pod inside the current test namespace on the specified cluster.
// The pod will connect to remoteIP:TestPort over TCP, send sendString over the
// connection, and write the network response in the pod termination log, then
// exit with 0 status
func (np *NetworkPod) buildTCPCheckConnectorPod() {

	ncatConnectTimeout := 4 // seconds
	tcpCheckConnectorPod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "tcp-check-pod",
			Labels: map[string]string{
				TestAppLabel: "tcp-check-pod",
			},
		},
		Spec: v1.PodSpec{
			Affinity:      nodeAffinity(np.Config.Scheduling),
			RestartPolicy: v1.RestartPolicyNever,
			Containers: []v1.Container{
				{
					Name:  "tcp-check-connector",
					Image: "busybox",
					// We send the string 50 times to put more pressure on the TCP connection and avoid limited
					// resource environments from not sending at least some data before timeout.
					Command: []string{"sh", "-c", "for in in $(seq 50); do echo [dataplane] connector says $SEND_STRING; done | for i in $(seq $CONN_TRIES); do if nc -v $REMOTE_IP $REMOTE_PORT -w $CONN_TIMEOUT; then break; fi; done >/dev/termination-log 2>&1"},
					Env: []v1.EnvVar{
						{Name: "REMOTE_PORT", Value: strconv.Itoa(np.Config.Port)},
						{Name: "SEND_STRING", Value: np.Config.Data},
						{Name: "REMOTE_IP", Value: np.Config.RemoteIP},
						{Name: "CONN_TRIES", Value: strconv.Itoa(np.ConnectionTimeout / ncatConnectTimeout)},
						{Name: "CONN_TIMEOUT", Value: strconv.Itoa(ncatConnectTimeout)},
					},
				},
			},
		},
	}

	pc := np.framework.ClusterClients[np.Config.Cluster].CoreV1().Pods(np.framework.Namespace)
	var err error
	np.Pod, err = pc.Create(&tcpCheckConnectorPod)
	Expect(err).NotTo(HaveOccurred())
}

func nodeAffinity(scheduling NetworkPodScheduling) *v1.Affinity {
	var nodeSelTerms []v1.NodeSelectorTerm

	switch scheduling {
	case GatewayNode:
		nodeSelTerms = addNodeSelectorTerm(nodeSelTerms, GatewayLabel, v1.NodeSelectorOpIn, []string{"true"})

	case NonGatewayNode:
		nodeSelTerms = addNodeSelectorTerm(nodeSelTerms, GatewayLabel, v1.NodeSelectorOpDoesNotExist, nil)
		nodeSelTerms = addNodeSelectorTerm(nodeSelTerms, GatewayLabel, v1.NodeSelectorOpNotIn, []string{"true"})
	}

	return &v1.Affinity{
		NodeAffinity: &v1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
				NodeSelectorTerms: nodeSelTerms,
			},
		},
	}
}

func addNodeSelectorTerm(nodeSelTerms []v1.NodeSelectorTerm, label string,
	op v1.NodeSelectorOperator, values []string) []v1.NodeSelectorTerm {
	return append(nodeSelTerms, v1.NodeSelectorTerm{MatchExpressions: []v1.NodeSelectorRequirement{
		{
			Key:      label,
			Operator: op,
			Values:   values,
		},
	}})
}

func removeDupDataplaneLines(output string) string {
	var newLines []string
	var lastLine string
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "[dataplane]") || line != lastLine {
			newLines = append(newLines, line)
		}

		lastLine = line
	}

	return strings.Join(newLines, "\n")
}
