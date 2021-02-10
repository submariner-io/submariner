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
package kubeproxy

import (
	"strconv"

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
