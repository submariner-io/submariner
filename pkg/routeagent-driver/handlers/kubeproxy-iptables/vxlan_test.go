package kp_iptables

import (
	"strconv"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("getVxlanVtepIPAddress", func() {
	Describe("Unit tests for getVxlanVtepIPAddress", func() {
		Context("When a valid ipaddress is provided to getVxlanVtepIPAddress", func() {
			It("Should return the VxLAN VtepIP that can be configured", func() {
				routeController := SyncHandler{}
				vtepIP, _ := routeController.getVxlanVtepIPAddress("192.168.100.24")
				Expect(vtepIP.String()).Should(Equal(strconv.Itoa(VxLANVTepNetworkPrefix) + ".168.100.24"))
			})
		})

		Context("When an invalid ipaddress is provided to getVxlanVtepIPAddress", func() {
			It("Should return an error", func() {
				routeController := SyncHandler{}
				_, err := routeController.getVxlanVtepIPAddress("10.0.0")
				Expect(err).ShouldNot(Equal(nil))
			})
		})
	})
})

func TestRoute(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Route Suite")
}
