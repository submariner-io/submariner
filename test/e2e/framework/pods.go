package framework

import (
    "fmt"
    "time"

    v1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/util/wait"

    . "github.com/onsi/gomega"
)

func (f *Framework) WaitForPodToBeReady(waitedPod *v1.Pod, cluster int) *v1.Pod {
    var finalPod *v1.Pod
    pc := f.ClusterClients[cluster].CoreV1().Pods(f.Namespace)
    err := wait.PollImmediate(5*time.Second, 2*time.Minute, func() (bool, error) {
        pod, err := pc.Get(waitedPod.Name, metav1.GetOptions{})
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
    return finalPod
}

func (f *Framework) WaitForPodFinishStatus(waitedPod *v1.Pod, cluster int) (terminationCode int32, terminationMessage string) {
    pc := f.ClusterClients[cluster].CoreV1().Pods(f.Namespace)
    err := wait.PollImmediate(5*time.Second, 2*time.Minute, func() (bool, error) {
        pod, err := pc.Get(waitedPod.Name, metav1.GetOptions{})
        if err != nil {
            if IsTransientError(err) {
                Logf("Transient failure when attempting to list pods: %v", err)
                return false, nil // return nil to avoid PollImmediate from stopping
            }
            return false, err
        }
        switch pod.Status.Phase {
            case v1.PodSucceeded:
                terminationCode = pod.Status.ContainerStatuses[0].State.Terminated.ExitCode
                terminationMessage = pod.Status.ContainerStatuses[0].State.Terminated.Message
                return true, nil
            case v1.PodFailed:
                terminationCode = pod.Status.ContainerStatuses[0].State.Terminated.ExitCode
                terminationMessage = pod.Status.ContainerStatuses[0].State.Terminated.Message
                return true, nil
            default:
                return false, nil
        }
    })
    Expect(err).NotTo(HaveOccurred())
    return terminationCode, terminationMessage
}
