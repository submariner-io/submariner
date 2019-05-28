package routecontroller_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	routecontroller "github.com/rancher/submariner/pkg/routeagent/controllers/route"
)

var _ = Describe("Route", func() {
	Describe("Unit test for router.go", func() {
		var rc *routecontroller.RouteController = &routecontroller.RouteController{}
		rc.clusterId
		rc.
			Context("Testing for function populateCidrBlockList", func() {
				It("should append cidrblock to subnet if subnets doesnot contain cidrblock", func() {
					var testinputcidrblocs = []string{"10.10.10.0/24"}
					var testsubnet = []string{"192.168.0.1/23"}
					//rc.populateCidrBlockList(testinputcidrblocks)

				})

				It("Should not append if subnets contain cidrblock", func() {

				})

			})
	})

})

//func (r *RouteController) populateCidrBlockList(inputCidrBlocks []string) {
//	for _, cidrBlock := range inputCidrBlocks {
//		if !containsString(r.subnets, cidrBlock) {
//			r.subnets = append(r.subnets, cidrBlock)
//		}
//	}
//}

//type RouteController struct {
//clusterId    string
//objectNamespace    string
//submarinerClientSet    clientset.Interface
//clustersSynced        cache.InformerSynced
//endpointsSynced        cache.InformerSynced
//clusterWorkqueue    workqueue.RateLimitingInterface
//endpointWorkqueue    workqueue.RateLimitingInterface
//gw    net.IP
//subnets    []string
//link    *net.Interface
//}

//func (r *RouteController) enqueueCluster(obj interface{}) {
//	var key string
//	var err error
//	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
//		utilruntime.HandleError(err)
//		return
//	}
//	klog.V(4).Infof("Enqueueing cluster for route controller %v", obj)
//	r.clusterWorkqueue.AddRateLimited(key)
//}
