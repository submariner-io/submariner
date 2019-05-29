package routecontroller

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("RouteController", func() {

	Describe("Function populateCidrBlockList", func() {

		Context("subnets does not contain CIDR blocks", func() {
			It("should append CIDR block to subnets", func() {
				routeController := RouteController{subnets: []string{"192.168.1.0/24"}}
				routeController.populateCidrBlockList([]string{"10.10.10.0/24", "192.168.1.0/24"})
				want := []string{"192.168.1.0/24", "10.10.10.0/24"}
				Expect(routeController.subnets).To(Equal(want))
			})
		})

		Context("subnets contain CIDR block", func() {
			It("Should not append CIDR block subnets", func() {
				routeController := RouteController{subnets: []string{"10.10.10.0/24", "192.168.1.0/24", "192.168.0.0./24"}}
				routeController.populateCidrBlockList([]string{"10.10.10.0/24", "192.168.1.0/24"})
				want := []string{"10.10.10.0/24", "192.168.1.0/24", "192.168.0.0./24"}
				Expect(routeController.subnets).To(Equal(want))
			})

		})
	})

	Describe("Function containsString", func() {
		Context("Given array of strings contain specified string", func() {
			It("Should return true", func() {
				Expect(containsString([]string{"unit", "test"}, "unit")).To(BeTrue())
			})
		})

		Context("Given array of strings does not contain specified string", func() {
			It("Should return false", func() {
				Expect(containsString([]string{"unit", "test"}, "ginkgo")).To(BeFalse())
			})
		})
	})

})