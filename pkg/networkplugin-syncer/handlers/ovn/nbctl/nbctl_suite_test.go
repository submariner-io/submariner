package nbctl

import (
	"fmt"
	"net"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const (
	ip1   = "1.1.1.1"
	ip2   = "2.2.2.2"
	ip3   = "3.3.3.3"
	port  = "1234"
	proto = "ssl"
)

func fakeResolver(host string) ([]net.IP, error) {
	return []net.IP{net.ParseIP(ip1), net.ParseIP(ip2), net.ParseIP(ip3)}, nil
}

var _ = Describe("expandConnectionStringIPs", func() {
	It("should expand correctly", func() {
		connection, err := expandConnectionStringIPsDetail(proto+":some-host:"+port, fakeResolver)
		Expect(err).NotTo(HaveOccurred())
		Expect(connection).To(Equal(fmt.Sprintf("%s:%s:%s,%s:%s:%s,%s:%s:%s",
			proto, ip1, port, proto, ip2, port, proto, ip3, port)))
	})
})

func TestNbctl(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "NBCTL Test Suite")
}
