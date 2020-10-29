package datastoresyncer_test

import (
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var _ = Describe("Endpoint syncing", testEndpointSyncing)
var _ = Describe("Endpoint exclusivity", testEndpointExclusivity)

func testEndpointSyncing() {
	t := newTestDriver()

	Context("on startup", func() {
		It("should create a new Endpoint locally and sync to the broker", func() {
			awaitEndpoint(t.localEndpoints, &t.localEndpoint.Spec)
			awaitEndpoint(t.brokerEndpoints, &t.localEndpoint.Spec)
		})

		When("creation of the local Endpoint fails", func() {
			BeforeEach(func() {
				t.expectedRunErr = errors.New("mock Create error")
				t.localEndpoints.FailOnCreate = t.expectedRunErr
			})

			It("Run should return an error", func() {
			})
		})
	})

	When("a remote Endpoint is created, updated and deleted on the broker", func() {
		It("should correctly sync the local datastore", func() {
			awaitEndpoint(t.brokerEndpoints, &t.localEndpoint.Spec)

			endpoint := newEndpoint(&submarinerv1.EndpointSpec{
				CableName: fmt.Sprintf("submariner-cable-%s-10-253-1-2", otherClusterID),
				ClusterID: otherClusterID,
				Hostname:  "bruins",
				PrivateIP: "10-253-1-2",
				Subnets:   []string{"200.0.0.0/16", "20.0.0.0/14"},
			})

			test.CreateResource(t.brokerEndpoints, test.SetClusterIDLabel(endpoint, endpoint.Spec.ClusterID))
			awaitEndpoint(t.localEndpoints, &endpoint.Spec)

			endpoint.Spec.Hostname = "celtics"
			endpoint.Spec.Subnets = append(endpoint.Spec.Subnets, "201.0.0.0/16")
			test.UpdateResource(t.brokerEndpoints, endpoint)
			awaitEndpoint(t.localEndpoints, &endpoint.Spec)

			Expect(t.brokerEndpoints.Delete(endpoint.GetName(), nil)).To(Succeed())
			test.AwaitNoResource(t.localEndpoints, endpoint.GetName())
		})
	})

	When("a remote Endpoint is synced locally", func() {
		It("should not try to re-sync to the broker", func() {
			awaitEndpoint(t.brokerEndpoints, &t.localEndpoint.Spec)

			endpoint := newEndpoint(&submarinerv1.EndpointSpec{
				CableName: fmt.Sprintf("submariner-cable-%s-10-253-1-2", otherClusterID),
				ClusterID: otherClusterID,
			})

			name := test.CreateResource(t.localEndpoints, test.SetClusterIDLabel(endpoint, endpoint.Spec.ClusterID)).GetName()
			time.Sleep(500 * time.Millisecond)
			test.AwaitNoResource(t.brokerEndpoints, name)
		})
	})
}

func testEndpointExclusivity() {
	t := newTestDriver()

	When("an Endpoint initially exists that doesn't match the local Endpoint", func() {
		var existingEndpoint *submarinerv1.Endpoint

		BeforeEach(func() {
			existingEndpoint = newEndpoint(&submarinerv1.EndpointSpec{
				CableName: "submariner-cable-east-1-2-3-4",
				ClusterID: clusterID,
			})

			test.CreateResource(t.localEndpoints, existingEndpoint)
			test.CreateResource(t.brokerEndpoints, test.SetClusterIDLabel(existingEndpoint, clusterID))
		})

		It("should delete the Endpoint from the local datastore and the broker", func() {
			time.Sleep(500 * time.Millisecond)
			test.AwaitNoResource(t.localEndpoints, existingEndpoint.GetName())
			test.AwaitNoResource(t.brokerEndpoints, existingEndpoint.GetName())
		})

		When("deletion of the Endpoint from the local datastore fails", func() {
			BeforeEach(func() {
				t.expectedRunErr = errors.New("mock Delete error")
				t.localEndpoints.FailOnDelete = t.expectedRunErr
			})

			It("Run should return an error", func() {
			})
		})

		When("deletion of the Endpoint from the local datastore returns not found", func() {
			BeforeEach(func() {
				t.localEndpoints.FailOnDelete = apierrors.NewNotFound(schema.GroupResource{}, existingEndpoint.Spec.CableName)
			})

			It("should ignore it", func() {
				awaitEndpoint(t.brokerEndpoints, &t.localEndpoint.Spec)
			})
		})
	})

	When("an Endpoint initially exists that matches the local Endpoint", func() {
		BeforeEach(func() {
			test.CreateResource(t.localEndpoints, newEndpoint(&t.localEndpoint.Spec))
		})

		It("should not delete it", func() {
			time.Sleep(500 * time.Millisecond)
			awaitEndpoint(t.localEndpoints, &t.localEndpoint.Spec)
		})
	})

	When("an Endpoint from another cluster initially exists", func() {
		BeforeEach(func() {
			endpoint := newEndpoint(&submarinerv1.EndpointSpec{
				CableName: fmt.Sprintf("submariner-cable-%s-10-253-1-2", otherClusterID),
				ClusterID: otherClusterID,
			})

			test.CreateResource(t.localEndpoints, test.SetClusterIDLabel(endpoint, endpoint.Spec.ClusterID))
		})

		It("should not delete it", func() {
			time.Sleep(500 * time.Millisecond)
			awaitEndpoint(t.localEndpoints, &t.localEndpoint.Spec)
		})
	})
}
