package datastoresyncer_test

import (
	"time"

	. "github.com/onsi/ginkgo"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

var _ = Describe("", func() {
	Context("Cluster syncing", testClusterSyncing)
})

func testClusterSyncing() {
	t := newTestDriver()

	It("should create a new Cluster resource locally and sync to the central datastore", func() {
		awaitCluster(t.submarinerClusters, &t.localCluster.Spec)
		awaitCluster(t.datastore.clusters, &t.localCluster.Spec)
	})

	When("a remote Cluster resource is created locally", func() {
		It("should not sync it to the central datastore", func() {
			name := createCluster(t.submarinerClusters, &submarinerv1.ClusterSpec{
				ClusterID: otherClusterID,
			})

			time.Sleep(500 * time.Millisecond)
			awaitNoCluster(t.datastore.clusters, name)
		})
	})
}
