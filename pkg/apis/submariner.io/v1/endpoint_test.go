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

package v1_test

import (
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	k8snet "k8s.io/utils/net"
)

const (
	ipV4Addr  = "1.2.3.4"
	ipV6Addr  = "2001:db8:3333:4444:5555:6666:7777:8888"
	ipV4CIDR  = "10.16.1.0/24"
	ipV4CIDR2 = "10.32.1.0/24"
	ipV6CIDR  = "2002:0:0:1234::/64"
)

var _ = Describe("EndpointSpec", func() {
	Context("GenerateName", testGenerateName)
	Context("Equals", testEquals)
	Context("GetHealthCheckIP", testGetHealthCheckIP)
	Context("SetHealthCheckIP", testSetHealthCheckIP)
	Context("GetPublicIP", testGetPublicIP)
	Context("SetPublicIP", testSetPublicIP)
	Context("GetPrivateIP", testGetPrivateIP)
	Context("SetPrivateIP", testSetPrivateIP)
	Context("GetFamilyCableName", testGetFamilyCableName)
	Context("GetIPFamilies", testGetIPFamilies)
	Context("ParseSubnets", testParseSubnets)
	Context("ExtractSubnetsExcludingIP", testExtractSubnetsExcludingIP)
})

func testGenerateName() {
	When("the fields are valid", func() {
		It("should return <cluster ID>-<cable name>", func() {
			name, err := (&v1.EndpointSpec{
				ClusterID: "ClusterID",
				CableName: "CableName",
			}).GenerateName()

			Expect(err).ToNot(HaveOccurred())
			Expect(name).To(Equal("clusterid-cablename"))
		})
	})

	When("the ClusterID is empty", func() {
		It("should return an error", func() {
			_, err := (&v1.EndpointSpec{
				CableName: "CableName",
			}).GenerateName()

			Expect(err).To(HaveOccurred())
		})
	})

	When("the CableName is empty", func() {
		It("should return an error", func() {
			_, err := (&v1.EndpointSpec{
				ClusterID: "ClusterID",
			}).GenerateName()

			Expect(err).To(HaveOccurred())
		})
	})
}

func testEquals() {
	var spec *v1.EndpointSpec

	BeforeEach(func() {
		spec = &v1.EndpointSpec{
			ClusterID: "east",
			CableName: "submariner-cable-east-172-16-32-5",
			Hostname:  "my-host",
			Backend:   "libreswan",
		}
	})

	Context("with equal scalar fields", func() {
		Context("and nil BackendConfig maps", func() {
			It("should return true", func() {
				Expect(spec.Equals(spec.DeepCopy())).To(BeTrue())
			})
		})

		Context("and equal BackendConfig maps", func() {
			It("should return true", func() {
				spec.BackendConfig = map[string]string{"key": "aaa"}
				Expect(spec.Equals(spec.DeepCopy())).To(BeTrue())
			})
		})

		Context("and empty BackendConfig maps", func() {
			It("should return true", func() {
				spec.BackendConfig = map[string]string{}
				Expect(spec.Equals(spec.DeepCopy())).To(BeTrue())
			})
		})
	})

	Context("with differing ClusterID fields", func() {
		It("should return false", func() {
			other := spec.DeepCopy()
			other.ClusterID = "west"
			Expect(spec.Equals(other)).To(BeFalse())
		})
	})

	Context("with differing CableName fields", func() {
		It("should return false", func() {
			other := spec.DeepCopy()
			other.CableName = "submariner-cable-east-5-6-7-8"
			Expect(spec.Equals(other)).To(BeFalse())
		})
	})

	Context("with differing Hostname fields", func() {
		It("should return false", func() {
			other := spec.DeepCopy()
			other.Hostname = "other-host"
			Expect(spec.Equals(other)).To(BeFalse())
		})
	})

	Context("with differing Backend fields", func() {
		It("should return false", func() {
			other := spec.DeepCopy()
			other.Backend = "wireguard"
			Expect(spec.Equals(other)).To(BeFalse())
		})
	})

	Context("with differing BackendConfig maps", func() {
		It("should return false", func() {
			other := spec.DeepCopy()
			other.BackendConfig = map[string]string{"key": "bbb"}
			spec.BackendConfig = map[string]string{"key": "aaa"}
			Expect(spec.Equals(other)).To(BeFalse())
		})
	})
}

func testGetIP(ipsSetter func(*v1.EndpointSpec, []string, string), ipsGetter func(*v1.EndpointSpec, k8snet.IPFamily) string) {
	var (
		spec         *v1.EndpointSpec
		legacyIPv4IP string
		ips          []string
	)

	BeforeEach(func() {
		legacyIPv4IP = ""
		ips = []string{}
	})

	JustBeforeEach(func() {
		spec = &v1.EndpointSpec{}
		ipsSetter(spec, ips, legacyIPv4IP)
	})

	Context("IPv4", func() {
		When("an IPv4 address is present", func() {
			BeforeEach(func() {
				ips = []string{ipV6Addr, ipV4Addr}
			})

			It("should return the address", func() {
				Expect(ipsGetter(spec, k8snet.IPv4)).To(Equal(ipV4Addr))
			})
		})

		When("an IPv4 address is not present and the legacy IPv4 address is set", func() {
			BeforeEach(func() {
				ips = []string{ipV6Addr}
				legacyIPv4IP = ipV4Addr
			})

			It("should return the legacy address", func() {
				Expect(ipsGetter(spec, k8snet.IPv4)).To(Equal(ipV4Addr))
			})
		})

		When("an IPv4 address is not present and the legacy IPv4 address is not set", func() {
			It("should return empty string", func() {
				Expect(ipsGetter(spec, k8snet.IPv4)).To(BeEmpty())
			})
		})
	})

	Context("IPv6", func() {
		When("an IPv6 address is present", func() {
			BeforeEach(func() {
				ips = []string{ipV4Addr, ipV6Addr}
			})

			It("should return the address", func() {
				Expect(ipsGetter(spec, k8snet.IPv6)).To(Equal(ipV6Addr))
			})
		})

		When("an IPv6 address is not present", func() {
			BeforeEach(func() {
				ips = []string{ipV4Addr}
			})

			It("should return empty string", func() {
				Expect(ipsGetter(spec, k8snet.IPv6)).To(BeEmpty())
			})
		})
	})

	When("the specified family is IPFamilyUnknown", func() {
		BeforeEach(func() {
			ips = []string{ipV4Addr, ipV6Addr}
		})

		It("should return empty string", func() {
			Expect(ipsGetter(spec, k8snet.IPFamilyUnknown)).To(BeEmpty())
		})
	})
}

func testSetIP(initIPs func(*v1.EndpointSpec, []string), ipsSetter func(*v1.EndpointSpec, string),
	ipsGetter func(*v1.EndpointSpec) ([]string, string),
) {
	var (
		spec       *v1.EndpointSpec
		ipToSet    string
		initialIPs []string
	)

	BeforeEach(func() {
		spec = &v1.EndpointSpec{}
		initialIPs = []string{}
		ipToSet = ""
	})

	JustBeforeEach(func() {
		initIPs(spec, initialIPs)
		ipsSetter(spec, ipToSet)
	})

	verifyIPs := func(ips []string, legacyV4 string) {
		actualIPs, actualLegacy := ipsGetter(spec)
		Expect(actualIPs).To(Equal(ips))
		Expect(actualLegacy).To(Equal(legacyV4))
	}

	Context("IPv4", func() {
		BeforeEach(func() {
			ipToSet = ipV4Addr
		})

		When("no addresses are present", func() {
			It("should add the new address", func() {
				verifyIPs([]string{ipToSet}, ipToSet)
			})
		})

		When("no IPv4 address is present", func() {
			BeforeEach(func() {
				initialIPs = []string{ipV6Addr}
			})

			It("should add the new address", func() {
				verifyIPs([]string{ipV6Addr, ipToSet}, ipToSet)
			})
		})

		When("an IPv4 address is already present", func() {
			BeforeEach(func() {
				initialIPs = []string{"11.22.33.44"}
			})

			It("should update address", func() {
				verifyIPs([]string{ipToSet}, ipToSet)
			})
		})
	})

	Context("IPv6", func() {
		BeforeEach(func() {
			ipToSet = ipV6Addr
		})

		When("no addresses are present", func() {
			It("should add the new address", func() {
				verifyIPs([]string{ipToSet}, "")
			})
		})

		When("no IPv6 address is present", func() {
			BeforeEach(func() {
				initialIPs = []string{ipV4Addr}
			})

			It("should add the new address", func() {
				verifyIPs([]string{ipV4Addr, ipToSet}, "")
			})
		})

		When("an IPv6 address is already present", func() {
			BeforeEach(func() {
				initialIPs = []string{"1234:cb9:3333:4444:5555:6666:7777:8888"}
			})

			It("should update address", func() {
				verifyIPs([]string{ipToSet}, "")
			})
		})
	})

	When("the specified IP is empty", func() {
		BeforeEach(func() {
			initialIPs = []string{ipV6Addr}
		})

		It("should not add it", func() {
			verifyIPs([]string{ipV6Addr}, "")
		})
	})

	When("the specified IP is invalid", func() {
		BeforeEach(func() {
			ipToSet = "invalid"
		})

		It("should not add it", func() {
			verifyIPs([]string{}, "")
		})
	})
}

func testGetHealthCheckIP() {
	testGetIP(func(s *v1.EndpointSpec, ips []string, ipv4IP string) {
		s.HealthCheckIPs = ips
		s.HealthCheckIP = ipv4IP
	}, func(s *v1.EndpointSpec, family k8snet.IPFamily) string {
		return s.GetHealthCheckIP(family)
	})
}

func testSetHealthCheckIP() {
	testSetIP(func(s *v1.EndpointSpec, ips []string) {
		s.HealthCheckIPs = ips
	}, func(s *v1.EndpointSpec, ip string) {
		s.SetHealthCheckIP(ip)
	}, func(s *v1.EndpointSpec) ([]string, string) {
		return s.HealthCheckIPs, s.HealthCheckIP
	})
}

func testGetPublicIP() {
	testGetIP(func(s *v1.EndpointSpec, ips []string, ipv4IP string) {
		s.PublicIPs = ips
		s.PublicIP = ipv4IP
	}, func(s *v1.EndpointSpec, family k8snet.IPFamily) string {
		return s.GetPublicIP(family)
	})
}

func testSetPublicIP() {
	testSetIP(func(s *v1.EndpointSpec, ips []string) {
		s.PublicIPs = ips
	}, func(s *v1.EndpointSpec, ip string) {
		s.SetPublicIP(ip)
	}, func(s *v1.EndpointSpec) ([]string, string) {
		return s.PublicIPs, s.PublicIP
	})
}

func testGetPrivateIP() {
	testGetIP(func(s *v1.EndpointSpec, ips []string, ipv4IP string) {
		s.PrivateIPs = ips
		s.PrivateIP = ipv4IP
	}, func(s *v1.EndpointSpec, family k8snet.IPFamily) string {
		return s.GetPrivateIP(family)
	})
}

func testSetPrivateIP() {
	testSetIP(func(s *v1.EndpointSpec, ips []string) {
		s.PrivateIPs = ips
	}, func(s *v1.EndpointSpec, ip string) {
		s.SetPrivateIP(ip)
	}, func(s *v1.EndpointSpec) ([]string, string) {
		return s.PrivateIPs, s.PrivateIP
	})
}

func testGetFamilyCableName() {
	var spec *v1.EndpointSpec

	BeforeEach(func() {
		spec = &v1.EndpointSpec{
			CableName: "submariner-cable-east-172-16-32-5",
		}
	})

	Context("with IPv4 family", func() {
		It("should return the cable name with v4 suffix", func() {
			expected := spec.CableName + "-v4"
			result := spec.GetFamilyCableName(k8snet.IPv4)
			Expect(result).To(Equal(expected))
		})
	})

	Context("with IPv6 family", func() {
		It("should return the cable name with v6 suffix", func() {
			expected := spec.CableName + "-v6"
			result := spec.GetFamilyCableName(k8snet.IPv6)
			Expect(result).To(Equal(expected))
		})
	})
}

func testGetIPFamilies() {
	It("should return the correct families", func() {
		Expect((&v1.EndpointSpec{Subnets: []string{ipV4CIDR}}).GetIPFamilies()).To(Equal([]k8snet.IPFamily{k8snet.IPv4}))
		Expect((&v1.EndpointSpec{Subnets: []string{ipV6CIDR}}).GetIPFamilies()).To(Equal([]k8snet.IPFamily{k8snet.IPv6}))
		Expect((&v1.EndpointSpec{Subnets: []string{ipV6CIDR, ipV4CIDR}}).GetIPFamilies()).To(
			Equal([]k8snet.IPFamily{k8snet.IPv6, k8snet.IPv4}))
	})
}

func testParseSubnets() {
	It("should return the correct subnets", func() {
		subnets := (&v1.EndpointSpec{Subnets: []string{ipV4CIDR, ipV6CIDR}}).ParseSubnets(k8snet.IPv4)
		Expect(subnets).To(Equal([]net.IPNet{
			{
				IP:   []byte{0xa, 0x10, 0x1, 0x0},
				Mask: net.IPv4Mask(0xff, 0xff, 0xff, 0x00),
			},
		}))

		subnets = (&v1.EndpointSpec{Subnets: []string{ipV4CIDR, ipV6CIDR}}).ParseSubnets(k8snet.IPv6)
		Expect(subnets).To(Equal([]net.IPNet{
			{
				IP:   []byte{0x20, 0x2, 0x0, 0x0, 0x0, 0x0, 0x12, 0x34, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
				Mask: []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
			},
		}))
	})
}

func testExtractSubnetsExcludingIP() {
	var spec *v1.EndpointSpec

	BeforeEach(func() {
		spec = &v1.EndpointSpec{
			Subnets: []string{ipV4CIDR, ipV4CIDR2, ipV6CIDR},
		}
	})

	Context("with two IPv4 subnets and one IPv6 subnet", func() {
		When("an IPv4 address is provided", func() {
			Context("and it's present in an IPv4 subnet", func() {
				It("should return the other IPv4 subnet", func() {
					result := spec.ExtractSubnetsExcludingIP("10.16.1.0")
					Expect(result).To(Equal([]string{ipV4CIDR2}))
				})
			})

			Context("and it's not present in any IPv4 subnet", func() {
				It("should return both IPv4 subnets", func() {
					result := spec.ExtractSubnetsExcludingIP("192.168.1.1")
					Expect(result).To(Equal([]string{ipV4CIDR, ipV4CIDR2}))
				})
			})
		})

		When("an IPv6 address is provided", func() {
			Context("and it's present in the IPv6 subnet", func() {
				It("should return empty", func() {
					result := spec.ExtractSubnetsExcludingIP("2002:0:0:1234::")
					Expect(result).To(BeEmpty())
				})
			})

			Context("and it's not present in the IPv6 subnet", func() {
				It("should return the IPv6 subnet", func() {
					result := spec.ExtractSubnetsExcludingIP("3002::1234:abcd:ffff:c0a8:101")
					Expect(result).To(Equal([]string{ipV6CIDR}))
				})
			})
		})
	})
}
