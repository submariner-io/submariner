package ipsec

import (
	"bytes"
	"os"
	"testing"

	"github.com/kelseyhightower/envconfig"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Strongswan", func() {
	Describe("Charon port configuration", testCharonConfigPortsGen)
})

func testCharonConfigPortsGen() {
	Context("config rendering", func() {
		It("should render config correctly out of strongSwan parameters", func() {
			ss := strongSwan{ipSecIKEPort: "500", ipSecNATTPort: "4500"}
			buf := new(bytes.Buffer)

			err := ss.renderCharonConfigTemplate(buf)

			Expect(err).ShouldNot(HaveOccurred())

			Expect(buf.String()).To(ContainSubstring("port = 500"))
			Expect(buf.String()).To(ContainSubstring("port_nat_t = 4500"))
			Expect(buf.String()).To(ContainSubstring("make_before_break = yes"))
			Expect(buf.String()).To(ContainSubstring("ignore_acquire_ts = yes"))

		})
	})

	Context("environment variable processing", func() {
		It("should get the defaults from the specification definition", func() {
			checkEnvVarParsingPorts("500", "4500")
		})

		It("should get the right environment variable names", func() {
			const (
				ikePort        = "555"
				nattPort       = "4555"
				ikePortEnvVar  = "CE_IPSEC_IKEPORT"
				nattPortEnvVar = "CE_IPSEC_NATTPORT"
			)
			os.Setenv(ikePortEnvVar, ikePort)
			os.Setenv(nattPortEnvVar, nattPort)

			checkEnvVarParsingPorts(ikePort, nattPort)

			os.Unsetenv(ikePortEnvVar)
			os.Unsetenv(nattPortEnvVar)
		})
	})
}

func checkEnvVarParsingPorts(ikePort string, nattPort string) {
	ipSecSpec := specification{}
	err := envconfig.Process(ipsecSpecEnvVarPrefix, &ipSecSpec)
	Expect(err).NotTo(HaveOccurred())
	Expect(ipSecSpec.IKEPort).To(Equal(ikePort))
	Expect(ipSecSpec.NATTPort).To(Equal(nattPort))
}

func TestStrongswan(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Strongswan Suite")
}
