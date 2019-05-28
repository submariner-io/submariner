package routecontroller

import (
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Route", func() {
	Describe("Unit test for router.go", func() {
		var rc RouteController
		var testinputcidrblocks []string

		BeforeEach(func() {
			testinputcidrblocks = []string{"10.10.10.0/24", "192.168.1.0/24"}
		})

		Context("Testing for function populateCidrBlockList", func() {
			It("should append cidrblock to subnet if subnets doesnot contain cidrblock", func() {
				rc.subnets = []string{"192.168.1.0/24"}
				rc.populateCidrBlockList(testinputcidrblocks)
				want := []string{"192.168.1.0/24", "10.10.10.0/24"}
				Expect(rc.subnets).To(Equal(want))
			})

			It("Should not append if subnets contain cidrblock", func() {
				rc.subnets = []string{"10.10.10.0/24", "192.168.1.0/24", "192.168.0.0./24"}
				rc.populateCidrBlockList(testinputcidrblocks)
				want := []string{"10.10.10.0/24", "192.168.1.0/24", "192.168.0.0./24"}
				Expect(rc.subnets).To(Equal(want))
				fmt.Println("nonono")
			})

		})
	})

})
