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
	"k8s.io/apimachinery/pkg/util/wait"
)

var _ = Describe("", func() {
	Context("Local Cluster syncing", testLocalClusterSyncing)
	Context("Remote Cluster syncing", testRemoteClusterSyncing)
})

func testLocalClusterSyncing() {
	var d *testDriver

	BeforeEach(func() {
		d = newTestDiver()
	})

	JustBeforeEach(func() {
		d.run()
	})

	AfterEach(func() {
		d.stop(nil)
	})

	It("should create the Cluster object locally and sync to the central datastore", func() {
		var foundCluster *submarinerv1.Cluster
		err := wait.PollImmediate(50*time.Millisecond, 5*time.Second, func() (bool, error) {
			cluster, err := d.submarinerClusters.Get(d.localCluster.Spec.ClusterID, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}

			foundCluster = cluster
			return true, nil
		})

		Expect(err).To(Succeed())
		Expect(foundCluster.Spec).To(Equal(d.localCluster.Spec))
		d.mockDatastore.VerifySetCluster(d.localCluster)
	})

	When("a remote Cluster is received", func() {
		It("should not sync it to the central datastore", func() {
			d.mockDatastore.VerifySetCluster(d.localCluster)

			_, err := d.submarinerClusters.Create(&submarinerv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: otherClusterID,
				},
			})
			Expect(err).To(Succeed())

			d.mockDatastore.VerifyNoSetCluster()
		})
	})

	When("the central datastore fails to sync a Cluster", func() {
		var expectedErr error

		BeforeEach(func() {
			expectedErr = d.mockDatastore.SetupErrOnFirstSetCluster()
		})

		It("should log the error", func() {
			Eventually(d.handledError, 5).Should(Receive(ContainErrorSubstring(expectedErr)))
		})
	})
}

func testRemoteClusterSyncing() {
	var (
		d               *testDriver
		remoteCluster   *types.SubmarinerCluster
		onClusterChange datastore.OnClusterChange
	)

	BeforeEach(func() {
		d = newTestDiver()
		remoteCluster = newSubmarinerCluster(otherClusterID)
	})

	JustBeforeEach(func() {
		d.run()
		onClusterChange = d.mockDatastore.VerifyWatchClusters()
	})

	AfterEach(func() {
		d.stop(nil)
	})

	When("invoked for a new remote Cluster that doesn't exist in the local datastore", func() {
		It("should be created in the local datastore", func() {
			Expect(onClusterChange(remoteCluster, false)).To(Succeed())

			localCluster, err := d.submarinerClusters.Get(getClusterName(remoteCluster), metav1.GetOptions{})
			Expect(err).To(Succeed())
			Expect(localCluster.Spec).To(Equal(remoteCluster.Spec))
		})
	})

	When("invoked for an updated remote Cluster that already exists in the local datastore", func() {
		It("should be updated in the local datastore", func() {
			clusterName := createCluster(remoteCluster, d.submarinerClusters)

			remoteCluster.Spec.ServiceCIDR = []string{"1.2.3.4/16"}
			Expect(onClusterChange(remoteCluster, false)).To(Succeed())

			localCluster, err := d.submarinerClusters.Get(clusterName, metav1.GetOptions{})
			Expect(err).To(Succeed())
			Expect(localCluster.Spec).To(Equal(remoteCluster.Spec))

			// Should be a no-op
			Expect(onClusterChange(remoteCluster, false)).To(Succeed())
		})
	})

	When("invoked for a deleted remote Cluster that exists in the local datastore", func() {
		It("should be deleted from the local datastore", func() {
			clusterName := createCluster(remoteCluster, d.submarinerClusters)

			Expect(onClusterChange(remoteCluster, true)).To(Succeed())

			_, err := d.submarinerClusters.Get(clusterName, metav1.GetOptions{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	When("invoked for a deleted remote Cluster that doesn't exist in the local datastore", func() {
		It("should succeed", func() {
			Expect(onClusterChange(remoteCluster, true)).To(Succeed())

			_, err := d.submarinerClusters.Get(getClusterName(remoteCluster), metav1.GetOptions{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	When("local datastore creation fails", func() {
		It("should return an error", func() {
			d.submarinerClusters.FailOnCreate = errors.New("mock Create error")
			Expect(onClusterChange(remoteCluster, false)).To(ContainErrorSubstring(d.submarinerClusters.FailOnCreate))
		})
	})

	When("local datastore update fails", func() {
		It("should return an error", func() {
			createCluster(remoteCluster, d.submarinerClusters)
			remoteCluster.Spec.ServiceCIDR = []string{"1.2.3.4/16"}

			d.submarinerClusters.FailOnUpdate = errors.New("mock Update error")
			Expect(onClusterChange(remoteCluster, false)).To(ContainErrorSubstring(d.submarinerClusters.FailOnUpdate))
		})
	})

	When("local datastore delete fails", func() {
		It("should return an error", func() {
			createCluster(remoteCluster, d.submarinerClusters)
			d.submarinerClusters.FailOnDelete = errors.New("mock Delete error")
			Expect(onClusterChange(remoteCluster, true)).To(ContainErrorSubstring(d.submarinerClusters.FailOnDelete))
		})
	})

	When("local datastore retrieval fails", func() {
		It("should return an error", func() {
			d.submarinerClusters.FailOnGet = errors.New("mock Get error")
			Expect(onClusterChange(remoteCluster, false)).To(ContainErrorSubstring(d.submarinerClusters.FailOnGet))
		})
	})
}
