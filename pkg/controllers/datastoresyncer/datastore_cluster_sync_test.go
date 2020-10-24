package datastoresyncer_test

import (
	"errors"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

var _ = Describe("Cluster syncing", testClusterSyncing)

func testClusterSyncing() {
	t := newTestDriver()

	Context("on startup", func() {
		It("should create a new Cluster locally and sync to the broker", func() {
			awaitCluster(t.localClusters, &t.localCluster.Spec)
			awaitCluster(t.brokerClusters, &t.localCluster.Spec)
		})

		When("a local Cluster already exists", func() {
			BeforeEach(func() {
				test.CreateResource(t.localClusters, newCluster(&t.localCluster.Spec))

				t.localCluster.Spec.GlobalCIDR = []string{"10.1.2.0/32"}
				t.localCluster.Spec.ServiceCIDR = append(t.localCluster.Spec.ServiceCIDR, "101.1.0.0/16")
			})

			It("should update it locally and sync to the broker", func() {
				awaitCluster(t.localClusters, &t.localCluster.Spec)
				awaitCluster(t.brokerClusters, &t.localCluster.Spec)
			})
		})

		When("creation of the local Cluster fails", func() {
			BeforeEach(func() {
				t.expectedRunErr = errors.New("mock Create error")
				t.localClusters.FailOnCreate = t.expectedRunErr
			})

			It("Run should return an error", func() {
			})
		})
	})

	When("a local Cluster is deleted", func() {
		It("should delete it from the broker", func() {
			awaitCluster(t.brokerClusters, &t.localCluster.Spec)

			name := t.localCluster.Spec.ClusterID
			Expect(t.localClusters.Delete(name, nil)).To(Succeed())
			test.AwaitNoResource(t.brokerClusters, name)
		})
	})

	When("a remote Cluster is created, updated and deleted on the broker", func() {
		It("should correctly sync the local datastore", func() {
			awaitCluster(t.brokerClusters, &t.localCluster.Spec)

			cluster := newCluster(&submarinerv1.ClusterSpec{
				ClusterID:   otherClusterID,
				ServiceCIDR: []string{"200.0.0.0/16"},
			})

			test.CreateResource(t.brokerClusters, test.SetClusterIDLabel(cluster, cluster.Spec.ClusterID))
			awaitCluster(t.localClusters, &cluster.Spec)

			cluster.Spec.ClusterCIDR = []string{"300.0.0.0/14"}
			cluster.Spec.ServiceCIDR = append(cluster.Spec.ServiceCIDR, "201.0.0.0/16")
			test.UpdateResource(t.brokerClusters, cluster)
			awaitCluster(t.localClusters, &cluster.Spec)

			Expect(t.brokerClusters.Delete(cluster.GetName(), nil)).To(Succeed())
			test.AwaitNoResource(t.localClusters, cluster.GetName())
		})
	})

	When("a remote Cluster is synced locally", func() {
		It("should not try to re-sync to the broker", func() {
			awaitCluster(t.brokerClusters, &t.localCluster.Spec)

			cluster := newCluster(&submarinerv1.ClusterSpec{
				ClusterID: otherClusterID,
			})

			name := test.CreateResource(t.localClusters, test.SetClusterIDLabel(cluster, cluster.Spec.ClusterID)).GetName()
			time.Sleep(500 * time.Millisecond)
			test.AwaitNoResource(t.brokerClusters, name)
		})
	})
}
