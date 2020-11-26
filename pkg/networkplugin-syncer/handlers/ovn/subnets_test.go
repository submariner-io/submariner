package ovn

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/util"
)

const cluster1Net1 = "10.0.0.0/24"
const cluster1Net2 = "10.0.1.0/24"
const cluster2Net1 = "20.0.0.0/24"
const cluster2Net2 = "20.0.1.0/24"

const localNet1 = "1.0.0.0/16"
const localNet2 = "1.1.0.0/16"

const unknownNet1 = "30.0.0.0/10"

var _ = Describe("Remote subnet handling", func() {
	var (
		ovn           *SyncHandler
		remoteSubnets *util.StringSet
		localSubnets  *util.StringSet
	)

	BeforeEach(func() {
		ovn = createHandlerWithTestEndpoints()
		remoteSubnets = util.NewStringSet(cluster1Net1, cluster1Net2, cluster2Net1, cluster2Net2)
		localSubnets = util.NewStringSet(localNet1, localNet2)
	})

	When("Handling remote endpoints", func() {
		It("should return no changes if there were none", func() {
			toAdd, toRemove := ovn.getNorthSubnetsToAddAndRemove(remoteSubnets)
			Expect(toAdd).To(BeEmpty())
			Expect(toRemove).To(BeEmpty())
		})

		It("should return missing elements to add", func() {
			remoteSubnets.Delete(cluster1Net2)
			toAdd, toRemove := ovn.getNorthSubnetsToAddAndRemove(remoteSubnets)
			Expect(toAdd).To(Equal([]string{cluster1Net2}))
			Expect(toRemove).To(BeEmpty())
		})

		It("should return unexpected elements to remove", func() {
			remoteSubnets.Add(unknownNet1)
			toAdd, toRemove := ovn.getNorthSubnetsToAddAndRemove(remoteSubnets)
			Expect(toAdd).To(BeEmpty())
			Expect(toRemove).To(Equal([]string{unknownNet1}))
		})
	})

	When("Handling local endpoints", func() {
		It("should return no changes if there were none", func() {
			toAdd, toRemove := ovn.getSouthSubnetsToAddAndRemove(localSubnets)
			Expect(toAdd).To(BeEmpty())
			Expect(toRemove).To(BeEmpty())
		})

		It("should return missing elements to add", func() {
			localSubnets.Delete(localNet1)
			toAdd, toRemove := ovn.getSouthSubnetsToAddAndRemove(localSubnets)
			Expect(toAdd).To(Equal([]string{localNet1}))
			Expect(toRemove).To(BeEmpty())
		})

		It("should return unexpected elements to remove", func() {
			localSubnets.Add(unknownNet1)
			toAdd, toRemove := ovn.getSouthSubnetsToAddAndRemove(localSubnets)
			Expect(toAdd).To(BeEmpty())
			Expect(toRemove).To(Equal([]string{unknownNet1}))
		})
	})
})

func createHandlerWithTestEndpoints() *SyncHandler {
	return &SyncHandler{
		remoteEndpoints: map[string]*v1.Endpoint{
			"endpoint1": {Spec: v1.EndpointSpec{
				ClusterID: "cluster-1",
				Subnets: []string{
					cluster1Net1,
					cluster1Net2,
				},
			}},
			"endpoint2": {Spec: v1.EndpointSpec{
				ClusterID: "cluster-2",
				Subnets: []string{
					cluster2Net1,
					cluster2Net2},
			}},
		},
		localEndpoint: &v1.Endpoint{
			Spec: v1.EndpointSpec{
				ClusterID: "cluster-1",
				Subnets: []string{
					localNet1,
					localNet2,
				},
			},
		},
	}
}
