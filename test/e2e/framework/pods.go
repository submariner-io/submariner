package framework

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (f *Framework) AwaitPodsByAppLabel(cluster ClusterIndex, appName string, namespace string, expectedCount int) *v1.PodList {
	return AwaitUntil("find pods for app "+appName, func() (interface{}, error) {
		return f.ClusterClients[cluster].CoreV1().Pods(namespace).List(metav1.ListOptions{
			LabelSelector: "app=" + appName,
		})
	}, func(result interface{}) (bool, error) {
		pods := result.(*v1.PodList)
		if expectedCount < 0 || len(pods.Items) == expectedCount {
			return true, nil
		}

		Logf("Actual pod count %d does not match the expected pod count %d - retrying...", len(pods.Items), expectedCount)
		return false, nil
	}).(*v1.PodList)
}

func (f *Framework) AwaitSubmarinerEnginePod(cluster ClusterIndex) *v1.Pod {
	return &f.AwaitPodsByAppLabel(cluster, SubmarinerEngine, TestContext.SubmarinerNamespace, 1).Items[0]
}

func (f *Framework) DeletePod(cluster ClusterIndex, podName string, namespace string) {
	AwaitUntil("list pods", func() (interface{}, error) {
		return nil, f.ClusterClients[cluster].CoreV1().Pods(namespace).Delete(podName, &metav1.DeleteOptions{})
	}, NoopCheckResult)
}
