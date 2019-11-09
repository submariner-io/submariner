package framework

import (
	. "github.com/onsi/gomega"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type CheckEndpointFunc func(endpoint *submarinerv1.Endpoint) (bool, string, error)

func (f *Framework) createSubmarinerClient(context string) *submarinerClientset.Clientset {
	restConfig := f.createRestConfig(context)
	clientSet, err := submarinerClientset.NewForConfig(restConfig)
	Expect(err).NotTo(HaveOccurred())
	return clientSet
}

func NoopCheckEndpoint(endpoint *submarinerv1.Endpoint) (bool, string, error) {
	return true, "", nil
}

func (f *Framework) AwaitSubmarinerEndpoint(cluster ClusterIndex, checkEndpoint CheckEndpointFunc) *submarinerv1.Endpoint {
	var retEndpoint *submarinerv1.Endpoint
	AwaitUntil("find the submariner endpoint for "+TestContext.KubeContexts[cluster], func() (interface{}, error) {
		return f.SubmarinerClients[cluster].SubmarinerV1().Endpoints(TestContext.SubmarinerNamespace).List(metav1.ListOptions{})
	}, func(result interface{}) (bool, string, error) {
		endpoints := result.(*submarinerv1.EndpointList)
		retEndpoint = nil
		for i := range endpoints.Items {
			if endpoints.Items[i].Spec.ClusterID == TestContext.KubeContexts[cluster] {
				if retEndpoint == nil {
					retEndpoint = &endpoints.Items[i]
				} else {
					// We expect one Endpoint at a time per cluster
					return false, "Multiple endpoints found", nil
				}
			}
		}

		if retEndpoint == nil {
			return false, "No endpoint found", nil
		}

		return checkEndpoint(retEndpoint)
	})

	return retEndpoint
}
