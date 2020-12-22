/*
© 2021 Red Hat, Inc. and others

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package libreswan

import (
	"os"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/submariner-io/submariner/pkg/types"
)

var _ = Describe("Libreswan", func() {
	Describe("IPsec port configuration", testIPsecPortConfiguration)
})

func testIPsecPortConfiguration() {
	When("NewLibreswan is called with no port environment variables set", func() {
		It("should set the port fields from the defaults in the specification definition", func() {
			checkLibreswanPorts(defaultIKEPort, defaultNATTPort)
		})
	})

	When("NewLibreswan is called with port environment variables set", func() {
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
			checkLibreswanPorts(ikePort, nattPort)
		})
	})
}

func createLibreswan() *libreswan {
	ls, err := NewLibreswan(types.SubmarinerEndpoint{}, types.SubmarinerCluster{})
	Expect(err).NotTo(HaveOccurred())

	return ls.(*libreswan)
}

func checkLibreswanPorts(ikePort, nattPort string) {
	ls := createLibreswan()
	Expect(ls.ipSecIKEPort).To(Equal(ikePort))
	Expect(ls.ipSecNATTPort).To(Equal(nattPort))
}
