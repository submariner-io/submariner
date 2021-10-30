/*
SPDX-License-Identifier: Apache-2.0

Copyright Contributors to the Submariner project.

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
	Describe("trafficStatusRE", testTrafficStatusRE)
})

func testTrafficStatusRE() {
	When("Parsing a normal connection", func() {
		It("should match", func() {
			matches := trafficStatusRE.FindStringSubmatch("006 #3: \"submariner-cable-cluster3-172-17-0-8-0-0\", " +
				"type=ESP, add_time=1590508783, inBytes=0, outBytes=0, id='172.17.0.8'\n")
			Expect(matches).NotTo(BeNil())
		})
	})

	When("Parsing a server-side connection", func() {
		It("should match", func() {
			matches := trafficStatusRE.FindStringSubmatch("006 #2: \"submariner-cable-cluster3-172-17-0-8-0-0\"[1] 3.139.75.179," +
				" type=ESP, add_time=1617195756, inBytes=0, outBytes=0, id='@10.0.63.203-0-0'\n")
			Expect(matches).NotTo(BeNil())
		})
	})
}

func testIPsecPortConfiguration() {
	When("NewLibreswan is called with no port environment variables set", func() {
		It("should set the port fields from the defaults in the specification definition", func() {
			checkLibreswanPort(defaultNATTPort)
		})
	})

	When("NewLibreswan is called with port environment variables set", func() {
		const (
			NATTPort       = "4555"
			NATTPortEnvVar = "CE_IPSEC_NATTPORT"
		)

		BeforeEach(func() {
			os.Setenv(NATTPortEnvVar, NATTPort)
		})

		AfterEach(func() {
			os.Unsetenv(NATTPortEnvVar)
		})

		It("should set the port fields from the environment variables", func() {
			checkLibreswanPort(NATTPort)
		})
	})
}

func createLibreswan() *libreswan {
	ls, err := NewLibreswan(&types.SubmarinerEndpoint{}, &types.SubmarinerCluster{})
	Expect(err).NotTo(HaveOccurred())

	return ls.(*libreswan)
}

func checkLibreswanPort(nattPort string) {
	ls := createLibreswan()
	Expect(ls.ipSecNATTPort).To(Equal(nattPort))
}
