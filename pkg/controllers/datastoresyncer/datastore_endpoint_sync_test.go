package datastoresyncer_test

import (
	"time"

	. "github.com/onsi/ginkgo"
	"github.com/pkg/errors"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var _ = Describe("", func() {
	Context("Endpoint syncing", testEndpointSyncing)
	Context("Failures while ensuring Endpoint exclusivity", testEndpointExclusivityFailures)
})

func testEndpointSyncing() {
	t := newTestDriver()

	It("should create a new Endpoint resource locally and sync to the central datastore", func() {
		awaitEndpoint(t.submarinerEndpoints, &t.localEndpoint.Spec)
		awaitEndpoint(t.datastore.endpoints, &t.localEndpoint.Spec)
	})

	When("a remote Endpoint resource is created locally", func() {
		It("should not sync it to the central datastore", func() {
			name := createEndpoint(t.submarinerEndpoints, &submarinerv1.EndpointSpec{
				CableName: "submariner-cable-" + otherClusterID + "-1-2-3-4",
				ClusterID: otherClusterID,
			})

			time.Sleep(500 * time.Millisecond)
			awaitNoEndpoint(t.datastore.endpoints, name)
		})
	})

	When("an Endpoint initially exists in the central datastore that doesn't match the local Endpoint", func() {
		var initialEndpoint *submarinerv1.Endpoint

		BeforeEach(func() {
			initialEndpoint = newEndpoint(&submarinerv1.EndpointSpec{
				CableName: "submariner-cable-east-1-2-3-4",
				ClusterID: clusterID,
			})

			t.initialLocalObjs = append(t.initialLocalObjs, initialEndpoint.DeepCopy())

			initialEndpoint.Namespace = brokerNamespace
			t.initialRemoteObjs = append(t.initialRemoteObjs, initialEndpoint)
		})

		It("should remove the Endpoint from the local and central datastores to ensure exclusivity", func() {
			awaitNoEndpoint(t.datastore.endpoints, initialEndpoint.Name)
			awaitNoEndpoint(t.submarinerEndpoints, initialEndpoint.Name)
		})
	})
}

func testEndpointExclusivityFailures() {
	t := newTestDriver()

	var (
		existingEndpoint *submarinerv1.Endpoint
	)

	BeforeEach(func() {
		existingEndpoint = newEndpoint(&submarinerv1.EndpointSpec{
			CableName: "submariner-cable-east-1-2-3-4",
			ClusterID: clusterID,
		})

		t.initialLocalObjs = append(t.initialLocalObjs, existingEndpoint.DeepCopy())

		existingEndpoint.Namespace = brokerNamespace
		t.initialRemoteObjs = append(t.initialRemoteObjs, existingEndpoint)
	})

	When("removal of the found Endpoint from the central datastore fails", func() {
		BeforeEach(func() {
			t.expectedRunErr = errors.New("mock RemoveEndpoint error")
			t.datastore.failOnRemoveEndpoint = t.expectedRunErr
		})

		It("should return an error", func() {
		})
	})

	When("the found Endpoint no longer exists in the central datastore", func() {
		BeforeEach(func() {
			t.datastore.failOnRemoveEndpoint = apierrors.NewNotFound(schema.GroupResource{}, existingEndpoint.Spec.CableName)
		})

		It("should succeed", func() {
			awaitEndpoint(t.datastore.endpoints, &t.localEndpoint.Spec)
		})
	})

	When("removal of the found Endpoint from the local datastore fails", func() {
		BeforeEach(func() {
			t.expectedRunErr = errors.New("mock Delete error")
			t.submarinerEndpoints.FailOnDelete = t.expectedRunErr
		})

		It("should return an error", func() {
		})
	})

	When("the found Endpoint does not exist in the local datastore", func() {
		BeforeEach(func() {
			t.submarinerEndpoints.FailOnDelete = apierrors.NewNotFound(schema.GroupResource{}, existingEndpoint.Spec.CableName)
		})

		It("should succeed", func() {
			awaitEndpoint(t.datastore.endpoints, &t.localEndpoint.Spec)
		})
	})
}
