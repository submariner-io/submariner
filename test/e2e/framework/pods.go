package framework

import (
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"

	. "github.com/onsi/gomega"
)

type TestPodType int

const (
	InvalidPodType TestPodType = iota
	ListenerPod
	ConnectorPod
)

type TestPodScheduling int

const (
	InvalidScheduling TestPodScheduling = iota
	GatewayNode
	NonGatewayNode
)

type TestPodConfig struct {
	Type       TestPodType
	Cluster    ClusterIndex
	Scheduling TestPodScheduling
	Port       int
	Data       string
	RemoteIP   string
	// TODO: namespace, once https://github.com/submariner-io/submariner/pull/141 is merged
}

type TestPod struct {
	Pod                *v1.Pod
	Config             *TestPodConfig
	TerminationCode    int32
	TerminationMessage string
	framework          *Framework
}

const (
	TestPort = 1234
)

func (f *Framework) NewTestPod(config *TestPodConfig) *TestPod {

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

	testPod := &TestPod{Config: config, framework: f, TerminationCode: -1}

	switch config.Type {
	case ListenerPod:
		testPod.buildTCPCheckListenerPod()
	case ConnectorPod:
		testPod.buildTCPCheckConnectorPod()
	}

	return testPod
}

func (tp *TestPod) WaitToBeReady() {
	var finalPod *v1.Pod
	pc := tp.framework.ClusterClients[tp.Config.Cluster].CoreV1().Pods(tp.framework.Namespace)
	err := wait.PollImmediate(5*time.Second, 2*time.Minute, func() (bool, error) {
		pod, err := pc.Get(tp.Pod.Name, metav1.GetOptions{})
		if err != nil {
			if IsTransientError(err) {
				Logf("Transient failure when attempting to list pods: %v", err)
				return false, nil // return nil to avoid PollImmediate from stopping
			}
			return false, err
		}
		if pod.Status.Phase != v1.PodRunning {
			if pod.Status.Phase != v1.PodPending {
				return false, fmt.Errorf("expected pod to be in phase \"Pending\" or \"Running\"")
			}
			return false, nil // pod is still pending
		}
		finalPod = pod
		return true, nil // pods is running
	})
	Expect(err).NotTo(HaveOccurred())
	tp.Pod = finalPod
}

func (tp *TestPod) WaitForFinishStatus() {

	finished := tp.WaitForFinish()

	Expect(finished).To(BeTrue())

	tp.TerminationCode = tp.Pod.Status.ContainerStatuses[0].State.Terminated.ExitCode
	tp.TerminationMessage = tp.Pod.Status.ContainerStatuses[0].State.Terminated.Message
}

func (tp *TestPod) WaitForFinish() bool {
	pc := tp.framework.ClusterClients[tp.Config.Cluster].CoreV1().Pods(tp.framework.Namespace)

	err := wait.PollImmediate(5*time.Second, 2*time.Minute, func() (bool, error) {
		var err error
		tp.Pod, err = pc.Get(tp.Pod.Name, metav1.GetOptions{})
		if err != nil {
			if IsTransientError(err) {
				Logf("Transient failure when attempting to list pods: %v", err)
				return false, nil // return nil to avoid PollImmediate from stopping
			}
			return false, err
		}
		switch tp.Pod.Status.Phase {
		case v1.PodSucceeded:
			return true, nil
		case v1.PodFailed:
			return true, nil
		default:
			return false, nil
		}
	})

	Expect(err).NotTo(HaveOccurred())

	return tp.Pod.Status.Phase == v1.PodSucceeded || tp.Pod.Status.Phase == v1.PodFailed
}

func (tp *TestPod) CreateService() *v1.Service {
	return tp.framework.CreateTCPService(tp.Config.Cluster, tp.Pod.Labels[TestAppLabel], tp.Config.Port)
}
