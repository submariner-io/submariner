package framework

import (
	"fmt"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (f *Framework) FindDeployment(cluster ClusterIndex, appName string, namespace string) *appsv1.Deployment {
	deployments := AwaitUntil("list deployments", func() (interface{}, error) {
		return f.ClusterClients[cluster].AppsV1().Deployments(namespace).List(metav1.ListOptions{
			LabelSelector: "app=" + appName,
		})
	}, NoopCheckResult).(*appsv1.DeploymentList)
	Expect(deployments.Items).To(HaveLen(1), fmt.Sprintf("Expected one %q deployment on %q",
		appName, TestContext.KubeContexts[cluster]))

	return &deployments.Items[0]
}
