package framework

import (
	"fmt"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AwaitPodsByAppLabel finds pods in a given cluster whose 'app' label value matches a specified value. If the specified
// expectedCount >= 0, the function waits until the number of pods equals the expectedCount.
func (f *Framework) AwaitPodsByAppLabel(cluster ClusterIndex, appName string, namespace string, expectedCount int) *v1.PodList {
	return AwaitUntil("find pods for app "+appName, func() (interface{}, error) {
		return f.ClusterClients[cluster].CoreV1().Pods(namespace).List(metav1.ListOptions{
			LabelSelector: "app=" + appName,
		})
	}, func(result interface{}) (bool, string, error) {
		pods := result.(*v1.PodList)
		if expectedCount < 0 || len(pods.Items) == expectedCount {
			return true, "", nil
		}

		return false, fmt.Sprintf("Actual pod count %d does not match the expected pod count %d", len(pods.Items), expectedCount), nil
	}).(*v1.PodList)
}

// AwaitSubmarinerEnginePod finds the submariner engine pod in a given cluster, waiting if necessary for a period of time
// for the pod to materialize.
func (f *Framework) AwaitSubmarinerEnginePod(cluster ClusterIndex) *v1.Pod {
	return &f.AwaitPodsByAppLabel(cluster, SubmarinerEngine, TestContext.SubmarinerNamespace, 1).Items[0]
}

// DeletePod deletes the pod for the given name and namespace.
func (f *Framework) DeletePod(cluster ClusterIndex, podName string, namespace string) {
	AwaitUntil("list pods", func() (interface{}, error) {
		return nil, f.ClusterClients[cluster].CoreV1().Pods(namespace).Delete(podName, &metav1.DeleteOptions{})
	}, NoopCheckResult)
}
