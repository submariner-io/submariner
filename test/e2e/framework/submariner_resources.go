/*
© 2021 Red Hat, Inc. and others

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package framework

import (
	"context"
	"fmt"

	. "github.com/onsi/gomega"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
)

type CheckEndpointFunc func(endpoint *submarinerv1.Endpoint) (bool, string, error)

func createSubmarinerClient(restConfig *rest.Config) *submarinerClientset.Clientset {
	clientSet, err := submarinerClientset.NewForConfig(restConfig)
	Expect(err).NotTo(HaveOccurred())

	return clientSet
}

func NoopCheckEndpoint(endpoint *submarinerv1.Endpoint) (bool, string, error) {
	return true, "", nil
}

func (f *Framework) AwaitSubmarinerEndpoint(cluster framework.ClusterIndex, checkEndpoint CheckEndpointFunc) *submarinerv1.Endpoint {
	var retEndpoint *submarinerv1.Endpoint

	framework.AwaitUntil("find the submariner endpoint for "+framework.TestContext.ClusterIDs[cluster], func() (interface{}, error) {
		return SubmarinerClients[cluster].SubmarinerV1().Endpoints(framework.TestContext.SubmarinerNamespace).List(
			context.TODO(), metav1.ListOptions{})
	}, func(result interface{}) (bool, string, error) {
		endpoints := result.(*submarinerv1.EndpointList)
		retEndpoint = nil
		for i := range endpoints.Items {
			if endpoints.Items[i].Spec.ClusterID == framework.TestContext.ClusterIDs[cluster] {
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

func (f *Framework) AwaitNewSubmarinerEndpoint(cluster framework.ClusterIndex, prevEndpointUID types.UID) *submarinerv1.Endpoint {
	return f.AwaitSubmarinerEndpoint(cluster, func(endpoint *submarinerv1.Endpoint) (bool, string, error) {
		if endpoint.ObjectMeta.UID != prevEndpointUID {
			return true, "", nil
		}

		return false, fmt.Sprintf("Expecting new Endpoint instance (UUID %q matches previous instance)", endpoint.ObjectMeta.UID), nil
	})
}

func (f *Framework) AwaitSubmarinerEndpointRemoved(cluster framework.ClusterIndex, endpointName string) {
	framework.AwaitUntil(fmt.Sprintf("await submariner Endpoint %q on %q removed", endpointName,
		framework.TestContext.ClusterIDs[cluster]),
		func() (interface{}, error) {
			_, err := SubmarinerClients[cluster].SubmarinerV1().Endpoints(framework.TestContext.SubmarinerNamespace).Get(
				context.TODO(), endpointName, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		},
		func(result interface{}) (bool, string, error) {
			return result.(bool), "", nil
		})
}
