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
