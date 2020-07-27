package strongswan

import (
	"bytes"
	"os"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/submariner-io/submariner/pkg/types"
)

var _ = Describe("Strongswan", func() {
	Describe("Charon port configuration", testCharonPortConfiguration)
})

func testCharonPortConfiguration() {
	When("renderCharonConfigTemplate is called", func() {
		It("should render the config file properties correctly from the strongSwan fields", func() {
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

	When("NewStrongSwan is called with no port environment variables set", func() {
		It("should set the port fields from the defaults in the specification definition", func() {
			checkStrongSwanPorts(defaultIKEPort, defaultNATTPort)
		})
	})

	When("NewStrongSwan is called with port environment variables set", func() {
		const (
			ikePort        = "555"
			nattPort       = "4555"
			ikePortEnvVar  = "CE_IPSEC_IKEPORT"
			nattPortEnvVar = "CE_IPSEC_NATTPORT"
		)

		BeforeEach(func() {
			os.Setenv(ikePortEnvVar, ikePort)
			os.Setenv(nattPortEnvVar, nattPort)
		})

		AfterEach(func() {
			os.Unsetenv(ikePortEnvVar)
			os.Unsetenv(nattPortEnvVar)
		})

		It("should set the port fields from the environment variables", func() {
			checkStrongSwanPorts(ikePort, nattPort)
		})
	})
}

func createStrongSwan() *strongSwan {
	ss, err := NewStrongSwan(types.SubmarinerEndpoint{}, types.SubmarinerCluster{})
	Expect(err).NotTo(HaveOccurred())

	return ss.(*strongSwan)
}

func checkStrongSwanPorts(ikePort, nattPort string) {
	ss := createStrongSwan()
	Expect(ss.ipSecIKEPort).To(Equal(ikePort))
	Expect(ss.ipSecNATTPort).To(Equal(nattPort))
}
