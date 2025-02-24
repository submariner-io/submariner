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

package cidr_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/pkg/cidr"
	k8snet "k8s.io/utils/net"
)

func TestCidr(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CIDR Suite")
}

const (
	ipV4CIDR = "1.2.3.4/16"
	ipV6CIDR = "2002::1234:abcd:ffff:c0a8:101/64"
)

var _ = Describe("ExtractIPFamilies", func() {
	It("should return the correct families", func() {
		Expect(cidr.ExtractIPFamilies([]string{ipV4CIDR})).To(Equal([]k8snet.IPFamily{k8snet.IPv4}))
		Expect(cidr.ExtractIPFamilies([]string{ipV6CIDR})).To(Equal([]k8snet.IPFamily{k8snet.IPv6}))
		Expect(cidr.ExtractIPFamilies([]string{ipV4CIDR, ipV6CIDR})).To(Equal([]k8snet.IPFamily{k8snet.IPv4, k8snet.IPv6}))
		Expect(cidr.ExtractIPFamilies([]string{ipV4CIDR, ipV4CIDR})).To(Equal([]k8snet.IPFamily{k8snet.IPv4}))
		Expect(cidr.ExtractIPFamilies([]string{})).To(BeEmpty())
		Expect(cidr.ExtractIPFamilies([]string{"bogus"})).To(BeEmpty())
	})
})
