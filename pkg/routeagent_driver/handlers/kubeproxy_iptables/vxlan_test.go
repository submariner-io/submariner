package kubeproxy_iptables

import (
	"strconv"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Function getVxlanVtepIPAddress tests", func() {
	When("a valid IP address is provided", func() {
		It("should return the VxLAN VtepIP that can be configured", func() {
			routeController := SyncHandler{}
			vtepIP, _ := routeController.getVxlanVtepIPAddress("192.168.100.24")
			Expect(vtepIP.String()).Should(Equal(strconv.Itoa(VxLANVTepNetworkPrefix) + ".168.100.24"))
		})
	})

	When("an invalid IP address is provided", func() {
		It("should return an error", func() {
			routeController := SyncHandler{}
			_, err := routeController.getVxlanVtepIPAddress("10.0.0")
			Expect(err).ShouldNot(Equal(nil))
		})
	})
})

func TestKubeProxyIPTables(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Kubeproxy IP Tables Suite")
}
