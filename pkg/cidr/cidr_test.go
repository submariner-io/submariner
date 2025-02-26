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
	ipV4CIDR        = "1.2.3.4/16"
	overlappingCIDR = "1.2.3.5/16"
	ipV4CIDR2       = "5.6.7.8/16"
	ipV4CIDR3       = "9.10.11.12/16"
	ipV6CIDR        = "2002::1234:abcd:ffff:c0a8:101/64"
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

var _ = Describe("ExtractIPv4Subnets", func() {
	It("should return the correct subnets", func() {
		Expect(cidr.ExtractIPv4Subnets([]string{ipV4CIDR, ipV6CIDR})).To(Equal([]string{ipV4CIDR}))
		Expect(cidr.ExtractIPv4Subnets([]string{ipV4CIDR, ipV4CIDR2})).To(Equal([]string{ipV4CIDR, ipV4CIDR2}))
		Expect(cidr.ExtractIPv4Subnets([]string{})).To(BeEmpty())
	})
})

var _ = Describe("IsOverlapping", func() {
	It("should return the correct results", func() {
		Expect(cidr.IsOverlapping([]string{ipV4CIDR, ipV6CIDR}, ipV4CIDR2)).To(BeFalse())
		Expect(cidr.IsOverlapping([]string{}, ipV4CIDR)).To(BeFalse())

		Expect(cidr.IsOverlapping([]string{ipV4CIDR}, overlappingCIDR)).To(BeTrue())
		Expect(cidr.IsOverlapping([]string{overlappingCIDR}, ipV4CIDR)).To(BeTrue())

		_, err := cidr.IsOverlapping([]string{"bogus"}, ipV4CIDR)
		Expect(err).To(HaveOccurred())

		_, err = cidr.IsOverlapping([]string{ipV4CIDR}, "bogus")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("OverlappingSubnets", func() {
	It("should return the correct result", func() {
		Expect(cidr.OverlappingSubnets([]string{ipV4CIDR}, []string{ipV4CIDR2}, []string{ipV4CIDR3})).To(Succeed())
		Expect(cidr.OverlappingSubnets([]string{ipV4CIDR, ipV4CIDR2}, []string{}, []string{ipV4CIDR3})).To(Succeed())
		Expect(cidr.OverlappingSubnets([]string{}, []string{ipV4CIDR, ipV4CIDR2}, []string{ipV4CIDR3})).To(Succeed())
		Expect(cidr.OverlappingSubnets([]string{"bogus"}, []string{"bogus"}, []string{ipV4CIDR3})).To(Succeed())

		Expect(cidr.OverlappingSubnets([]string{ipV4CIDR}, []string{ipV4CIDR2}, []string{overlappingCIDR})).ToNot(Succeed())
		Expect(cidr.OverlappingSubnets([]string{ipV4CIDR2}, []string{ipV4CIDR}, []string{overlappingCIDR})).ToNot(Succeed())
		Expect(cidr.OverlappingSubnets([]string{ipV4CIDR2, ipV4CIDR}, []string{ipV4CIDR3}, []string{overlappingCIDR})).ToNot(Succeed())
	})
})
