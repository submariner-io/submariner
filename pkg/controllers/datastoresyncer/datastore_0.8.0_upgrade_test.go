package datastoresyncer_test

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var _ = Describe("0.8.0 upgrade", func() {
	t := newTestDriver()

	When(fmt.Sprintf("a remote Endpoint exists locally with no %q label", federate.ClusterIDLabelKey), func() {
		var endpointName string

		BeforeEach(func() {
			endpointName = test.CreateResource(t.localEndpoints, newEndpoint(&submarinerv1.EndpointSpec{
				CableName: fmt.Sprintf("submariner-cable-%s-10-253-1-2", otherClusterID),
				ClusterID: otherClusterID,
			})).GetName()
		})

		It("should not try to sync it to the broker", func() {
			time.Sleep(500 * time.Millisecond)
			test.AwaitNoResource(t.brokerEndpoints, endpointName)
		})
	})

	When(fmt.Sprintf("a local Endpoint exists on the broker with no %q label", federate.ClusterIDLabelKey), func() {
		var endpointName string

		BeforeEach(func() {
			ep := newEndpoint(&t.localEndpoint.Spec)
			endpointName = test.CreateResource(t.localEndpoints, ep).GetName()
			test.CreateResource(t.brokerEndpoints, ep)
		})

		It("should update the label on the broker", func() {
			test.AwaitAndVerifyResource(t.brokerEndpoints, endpointName, func(obj *unstructured.Unstructured) bool {
				value, ok := obj.GetLabels()[federate.ClusterIDLabelKey]
				return ok && value == t.localEndpoint.Spec.ClusterID
			})
		})
	})

	When(fmt.Sprintf("a remote Cluster exists locally with no %q label", federate.ClusterIDLabelKey), func() {
		var clusterName string

		BeforeEach(func() {
			clusterName = test.CreateResource(t.localClusters, newCluster(&submarinerv1.ClusterSpec{
				ClusterID: otherClusterID,
			})).GetName()
		})

		It("should not try to sync it to the broker", func() {
			time.Sleep(500 * time.Millisecond)
			test.AwaitNoResource(t.brokerClusters, clusterName)
		})
	})
})
