package datastoresyncer_test

import (
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/datastore"
	. "github.com/submariner-io/submariner/pkg/gomega"
	"github.com/submariner-io/submariner/pkg/types"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
)

var _ = Describe("", func() {
	Context("Local Endpoint syncing", testLocalEndpointSyncing)
	Context("Remote Endpoint syncing", testRemoteEndpointSyncing)
	Context("Failures while ensuring Endpoint exclusivity", testEndpointExclusivityFailures)
})

func testLocalEndpointSyncing() {
	var d *testDriver

	BeforeEach(func() {
		d = newTestDriver()
	})

	JustBeforeEach(func() {
		d.run()
	})

	AfterEach(func() {
		d.stop(nil)
	})

	It("should create the Endpoint object locally and sync to the central datastore", func() {
		endpointName := getEndpointName(d.localEndpoint)

		var foundEndpoint *submarinerv1.Endpoint
		err := wait.PollImmediate(50*time.Millisecond, 5*time.Second, func() (bool, error) {
			endpoint, err := d.submarinerEndpoints.Get(endpointName, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}

			foundEndpoint = endpoint
			return true, nil
		})

		Expect(err).To(Succeed())
		Expect(foundEndpoint.Spec).To(Equal(d.localEndpoint.Spec))
		d.mockDatastore.VerifySetEndpoint(d.localEndpoint)
	})

	When("a remote Endpoint is received", func() {
		It("should not sync it to the central datastore", func() {
			d.mockDatastore.VerifySetEndpoint(d.localEndpoint)

			_, err := d.submarinerEndpoints.Create(newEndpoint(otherClusterID))
			Expect(err).To(Succeed())

			d.mockDatastore.VerifyNoSetEndpoint()
		})
	})

	When("an Endpoint matching the local Endpoint is received", func() {
		It("should not sync it to the central datastore", func() {
			d.mockDatastore.VerifySetEndpoint(d.localEndpoint)

			_, err := d.submarinerEndpoints.Create(newEndpoint(clusterID))
			Expect(err).To(Succeed())

			d.mockDatastore.VerifyNoSetEndpoint()
		})
	})

	When("the central datastore fails to sync an Endpoint", func() {
		var expectedErr error

		BeforeEach(func() {
			expectedErr = errors.New("mock SetEndpoint error")
			d.mockDatastore.SetupErrOnFirstSetEndpoint(expectedErr)
		})

		It("should log the error", func() {
			Eventually(d.handledError, 5).Should(Receive(ContainErrorSubstring(expectedErr)))
		})
	})

	When("an Endpoint initially exists in the central datastore that doesn't match the local Endpoint", func() {
		var initialEndpoint *submarinerv1.Endpoint
		var submarinerEndpoint *types.SubmarinerEndpoint

		BeforeEach(func() {
			submarinerEndpoint = &types.SubmarinerEndpoint{
				Spec: submarinerv1.EndpointSpec{
					CableName: "submariner-cable-east-1-2-3-4",
					ClusterID: clusterID,
				},
			}

			d.mockDatastore.SetupGetEndpoints(clusterID, nil, *submarinerEndpoint)

			initialEndpoint = &submarinerv1.Endpoint{
				ObjectMeta: metav1.ObjectMeta{
					Name:      getEndpointName(submarinerEndpoint),
					Namespace: namespace,
				},
			}

			d.initialObjs = append(d.initialObjs, initialEndpoint)
		})

		It("should remove the Endpoint from the local and central datastores to ensure exclusivity", func() {
			d.mockDatastore.VerifyRemoveEndpoint(submarinerEndpoint.Spec.ClusterID, submarinerEndpoint.Spec.CableName)

			_, err := d.submarinerEndpoints.Get(initialEndpoint.Name, metav1.GetOptions{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})
}

func testRemoteEndpointSyncing() {
	var (
		d                *testDriver
		remoteEndpoint   *types.SubmarinerEndpoint
		onEndpointChange datastore.OnEndpointChange
	)

	BeforeEach(func() {
		d = newTestDriver()
		remoteEndpoint = newSubmarinerEndpoint(otherClusterID)
	})

	JustBeforeEach(func() {
		d.run()
		onEndpointChange = d.mockDatastore.VerifyWatchEndpoints()
	})

	AfterEach(func() {
		d.stop(nil)
	})

	When("invoked for a new remote Endpoint that doesn't exist in the local datastore", func() {
		It("should be created in the local datastore", func() {
			Expect(onEndpointChange(remoteEndpoint, false)).To(Succeed())

			localEndpoint, err := d.submarinerEndpoints.Get(getEndpointName(remoteEndpoint), metav1.GetOptions{})
			Expect(err).To(Succeed())
			Expect(localEndpoint.Spec).To(Equal(remoteEndpoint.Spec))
		})
	})

	When("invoked for an updated remote Endpoint that already exists in the local datastore", func() {
		It("should be updated in the local datastore", func() {
			endpointName := createEndpoint(remoteEndpoint, d.submarinerEndpoints)
			remoteEndpoint.Spec.Subnets = []string{"1.2.3.4/16"}
			Expect(onEndpointChange(remoteEndpoint, false)).To(Succeed())

			localEndpoint, err := d.submarinerEndpoints.Get(endpointName, metav1.GetOptions{})
			Expect(err).To(Succeed())
			Expect(localEndpoint.Spec).To(Equal(remoteEndpoint.Spec))

			// Should be a no-op
			Expect(onEndpointChange(remoteEndpoint, false)).To(Succeed())
		})
	})

	When("invoked for a deleted remote Endpoint that exists in the local datastore", func() {
		It("should be deleted from the local datastore", func() {
			endpointName := createEndpoint(remoteEndpoint, d.submarinerEndpoints)
			Expect(onEndpointChange(remoteEndpoint, true)).To(Succeed())

			_, err := d.submarinerEndpoints.Get(endpointName, metav1.GetOptions{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	When("invoked for a deleted remote Endpoint that doesn't exist in the local datastore", func() {
		It("should succeed", func() {
			Expect(onEndpointChange(remoteEndpoint, true)).To(Succeed())

			_, err := d.submarinerEndpoints.Get(getEndpointName(remoteEndpoint), metav1.GetOptions{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	When("local datastore creation fails", func() {
		It("should return an error", func() {
			d.submarinerEndpoints.FailOnCreate = errors.New("mock Create error")
			Expect(onEndpointChange(remoteEndpoint, false)).To(ContainErrorSubstring(d.submarinerEndpoints.FailOnCreate))
		})
	})

	When("local datastore update fails", func() {
		It("should return an error", func() {
			createEndpoint(remoteEndpoint, d.submarinerEndpoints)
			remoteEndpoint.Spec.Subnets = []string{"1.2.3.4/16"}

			d.submarinerEndpoints.FailOnUpdate = errors.New("mock Update error")
			Expect(onEndpointChange(remoteEndpoint, false)).To(ContainErrorSubstring(d.submarinerEndpoints.FailOnUpdate))
		})
	})

	When("local datastore delete fails", func() {
		It("should return an error", func() {
			createEndpoint(remoteEndpoint, d.submarinerEndpoints)
			d.submarinerEndpoints.FailOnDelete = errors.New("mock Delete error")
			Expect(onEndpointChange(remoteEndpoint, true)).To(ContainErrorSubstring(d.submarinerEndpoints.FailOnDelete))
		})
	})

	When("local datastore retrieval fails", func() {
		It("should return an error", func() {
			d.submarinerEndpoints.FailOnGet = errors.New("mock Get error")
			Expect(onEndpointChange(remoteEndpoint, false)).To(ContainErrorSubstring(d.submarinerEndpoints.FailOnGet))
		})
	})
}

func testEndpointExclusivityFailures() {
	var (
		expectedErr      error
		d                *testDriver
		existingEndpoint *types.SubmarinerEndpoint
	)

	BeforeEach(func() {
		expectedErr = nil
		d = newTestDriver()
		existingEndpoint = &types.SubmarinerEndpoint{
			Spec: submarinerv1.EndpointSpec{
				CableName: "submariner-cable-east-1-2-3-4",
				ClusterID: clusterID,
			},
		}
	})

	JustBeforeEach(func() {
		d.run()
	})

	AfterEach(func() {
		d.stop(expectedErr)
	})

	When("retrieval of Endpoints from the central datastore fails", func() {
		BeforeEach(func() {
			expectedErr = errors.New("mock GetEndpoints error")
			d.mockDatastore.SetupGetEndpoints(clusterID, expectedErr)
		})

		It("should return an error", func() {
		})
	})

	When("removal of the found Endpoint from the central datastore fails", func() {
		BeforeEach(func() {
			d.mockDatastore.SetupGetEndpoints(clusterID, nil, *existingEndpoint)
			expectedErr = errors.New("mock RemoveEndpoint error")
			d.mockDatastore.SetupErrOnFirstRemoveEndpoint(expectedErr)
		})

		It("should return an error", func() {
		})
	})

	When("the found Endpoint no longer exists in the central datastore", func() {
		BeforeEach(func() {
			d.mockDatastore.SetupGetEndpoints(clusterID, nil, *existingEndpoint)
			d.mockDatastore.SetupErrOnFirstRemoveEndpoint(apierrors.NewNotFound(schema.GroupResource{}, existingEndpoint.Spec.CableName))
		})

		It("should succeed", func() {
			d.mockDatastore.VerifySetEndpoint(d.localEndpoint)
		})
	})

	When("removal of the found Endpoint from the local datastore fails", func() {
		BeforeEach(func() {
			d.mockDatastore.SetupGetEndpoints(clusterID, nil, *existingEndpoint)
			expectedErr = errors.New("mock Delete error")
			d.submarinerEndpoints.FailOnDelete = expectedErr
		})

		It("should return an error", func() {
		})
	})

	When("the found Endpoint does not exist in the local datastore", func() {
		BeforeEach(func() {
			d.mockDatastore.SetupGetEndpoints(clusterID, nil, *existingEndpoint)
			d.submarinerEndpoints.FailOnDelete = apierrors.NewNotFound(schema.GroupResource{}, existingEndpoint.Spec.CableName)
		})

		It("should succeed", func() {
			d.mockDatastore.VerifySetEndpoint(d.localEndpoint)
		})
	})
}
