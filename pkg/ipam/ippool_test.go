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

package ipam_test

import (
	"net"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/stringset"
	"github.com/submariner-io/submariner/pkg/ipam"
)

const cidrWithSize2 = "169.254.1.0/30"

var (
	_ = Describe("IP Pool creation", testPoolCreation)
	_ = Describe("IP Pool allocation", testPoolAllocation)
	_ = Describe("IP Pool release", testPoolRelease)
	_ = Describe("IP Pool reserve", testPoolReserve)
	_ = Describe("isContiguous", testIsContiguous)
)

func testPoolCreation() {
	When("the CIDR is missing the prefix", func() {
		It("should return an error", func() {
			pool, err := ipam.NewIPPool("169.254.0.0")
			Expect(err).To(HaveOccurred())
			Expect(pool).To(BeNil())
		})
	})

	When("the CIDR prefix is /33", func() {
		It("should return an error", func() {
			pool, err := ipam.NewIPPool("169.254.0.0/33")
			Expect(err).To(HaveOccurred())
			Expect(pool).To(BeNil())
		})
	})

	When("the CIDR Prefix is /32", func() {
		It("should return an error", func() {
			pool, err := ipam.NewIPPool("169.254.1.0/32")
			Expect(err).To(HaveOccurred())
			Expect(pool).To(BeNil())
		})
	})

	When("CIDR Prefix is /31", func() {
		It("should return an error", func() {
			pool, err := ipam.NewIPPool("169.254.1.0/31")
			Expect(err).To(HaveOccurred())
			Expect(pool).To(BeNil())
		})
	})

	When("CIDR Prefix is /30", func() {
		It("should create the pool with size 2", func() {
			pool, err := ipam.NewIPPool(cidrWithSize2)
			Expect(err).To(Succeed())
			Expect(pool.Size()).Should(Equal(2))
		})
	})

	When("CIDR Prefix is /24", func() {
		It("should create the pool with size 254", func() {
			pool, err := ipam.NewIPPool("169.254.1.0/24")
			Expect(err).To(Succeed())
			Expect(pool.Size()).Should(Equal(254))
		})
	})

	When("CIDR Prefix is /16", func() {
		It("should create the pool with size 65534", func() {
			pool, err := ipam.NewIPPool("169.254.1.0/16")
			Expect(err).To(Succeed())
			Expect(pool.Size()).Should(Equal(65534))
		})
	})
}

func testPoolAllocation() {
	t := newTestDriver()

	When("a single IP is requested", func() {
		It("should return an IP contained in the CIDR", func() {
			ip := t.allocate(1)[0]
			Expect(t.network.Contains(net.ParseIP(ip))).To(BeTrue())
			Expect(t.allocate(1)[0]).ToNot(Equal(ip))
		})

		Context("and the pool is exhausted", func() {
			BeforeEach(func() {
				t.cidr = cidrWithSize2
			})

			It("should return an error", func() {
				t.allocate(1)
				t.allocate(1)

				_, err := t.pool.Allocate(1)
				Expect(err).To(HaveOccurred())
			})
		})
	})

	When("a block is requested", func() {
		It("should return a contiguous list of IPs contained in the CIDR", func() {
			ips := t.allocate(10)
			verifyContiguous(ips)

			for _, ip := range ips {
				Expect(t.network.Contains(net.ParseIP(ip))).To(BeTrue())
			}
		})

		Context("followed by additional requests", func() {
			It("should return unique IPs per request", func() {
				ips := t.allocate(10)
				ips = append(ips, t.allocate(1)...)

				for i := 1; i <= 24; i++ {
					n := t.allocate(10)
					verifyContiguous(n)
					ips = append(ips, n...)

					if 1%10 == 0 {
						ips = append(ips, t.allocate(1)...)
					}
				}

				set := stringset.New()
				for _, ip := range ips {
					Expect(set.Add(ip)).To(BeTrue())
				}
			})
		})

		Context("and the pool is exhausted", func() {
			BeforeEach(func() {
				t.cidr = cidrWithSize2
			})

			It("should return an error", func() {
				t.allocate(2)

				_, err := t.pool.Allocate(2)
				Expect(err).To(HaveOccurred())
			})
		})
	})

	When("a request is too large for the CIDR", func() {
		BeforeEach(func() {
			t.cidr = cidrWithSize2
		})

		It("should return an error", func() {
			_, err := t.pool.Allocate(3)
			Expect(err).To(HaveOccurred())
		})
	})

	When("the number requested is zero", func() {
		It("should succeed", func() {
			t.allocate(0)
		})
	})

	When("the number requested is negative", func() {
		It("should return an error", func() {
			_, err := t.pool.Allocate(-1)
			Expect(err).To(HaveOccurred())
		})
	})
}

func testPoolRelease() {
	t := newTestDriver()

	When("a single IP is released after allocation", func() {
		BeforeEach(func() {
			t.cidr = cidrWithSize2
		})

		It("should be available for re-allocation", func() {
			t.allocate(1)
			ip := t.allocate(1)[0]
			Expect(t.pool.Release(ip)).To(Succeed())

			ip2 := t.allocate(1)[0]
			Expect(ip).To(Equal(ip2))
		})
	})

	When("an IP block is released after allocation", func() {
		It("should be available for re-allocation", func() {
			t.allocate(150)
			ips := t.allocate(50)

			_, err := t.pool.Allocate(100)
			Expect(err).To(HaveOccurred())

			Expect(t.pool.Release(ips...)).To(Succeed())
			t.allocate(100)
		})
	})

	When("sufficient blocks are released after the pool becomes exhausted", func() {
		Context("upon subsequent allocation", func() {
			It("should eventually find a sufficient block", func() {
				t.allocate(48)
				t.allocate(2)
				b1 := t.allocate(20) // 70
				t.allocate(15)       // 85
				b2 := t.allocate(5)  // 90
				b3 := t.allocate(17) // 107
				t.allocate(133)      // 240

				Expect(t.pool.Release(b1...)).To(Succeed())
				_, err := t.pool.Allocate(22)
				Expect(err).To(HaveOccurred())

				Expect(t.pool.Release(b2...)).To(Succeed())
				_, err = t.pool.Allocate(22)
				Expect(err).To(HaveOccurred())

				Expect(t.pool.Release(b3...)).To(Succeed())
				t.allocate(22)
			})
		})
	})

	When("an IP not in the CIDR range is released", func() {
		It("should return and error and not return it to the pool", func() {
			t.allocate(1)
			Expect(t.pool.Release("169.253.1.1")).To(HaveOccurred())

			ips, err := t.pool.Allocate(t.pool.Size())
			Expect(err).To(Succeed())
			Expect(stringset.New(ips...).Contains("169.253.1.1")).To(BeFalse())
		})
	})
}

func testPoolReserve() {
	t := newTestDriver()

	When("a block of IPs are available", func() {
		It("should reserve them", func() {
			err := t.pool.Reserve("169.254.1.1", "169.254.1.2", "169.254.1.3")
			Expect(err).To(Succeed())

			ips := t.allocate(251)
			Expect(ips[0]).To(Equal("169.254.1.4"))
			verifyContiguous(ips)
		})
	})

	When("an IP isn't available", func() {
		It("should return an error", func() {
			ips := t.allocate(3)
			err := t.pool.Reserve(ips...)
			Expect(err).To(HaveOccurred())
		})
	})

	When("an IP isn't in the CIDR range", func() {
		It("should return an error", func() {
			err := t.pool.Reserve("169.253.1.1")
			Expect(err).To(HaveOccurred())
		})
	})
}

func testIsContiguous() {
	When("contiguous", func() {
		It("should return true", func() {
			Expect(isContiguous([]string{"10.20.30.1", "10.20.30.2"})).To(BeTrue())
			Expect(isContiguous([]string{"10.20.30.1", "10.20.30.2", "10.20.30.3"})).To(BeTrue())
			Expect(isContiguous([]string{"1.2.3.255", "1.2.4.0"})).To(BeTrue())
		})
	})

	When("not contiguous", func() {
		It("should return false", func() {
			Expect(isContiguous([]string{"10.20.30.1", "10.20.30.3"})).To(BeFalse())
			Expect(isContiguous([]string{"10.20.30.2", "10.20.30.1"})).To(BeFalse())
			Expect(isContiguous([]string{"10.20.30.1", "10.20.30.2", "10.20.30.4"})).To(BeFalse())
			Expect(isContiguous([]string{"10.20.30.1", "10.20.31.2"})).To(BeFalse())
			Expect(isContiguous([]string{"1.2.3.255", "1.2.4.1"})).To(BeFalse())
		})
	})
}

func isContiguous(ips []string) bool {
	size := len(ips)
	for prev, curr := 0, 0; curr < size; prev, curr = curr, curr+1 {
		if curr == 0 {
			continue
		}

		if ipam.StringIPToInt(ips[prev])+1 != ipam.StringIPToInt(ips[curr]) {
			return false
		}
	}

	return true
}

func verifyContiguous(ips []string) {
	Expect(isContiguous(ips)).To(BeTrue(), "IPs are not contiguous: %v", ips)
}

type testDriver struct {
	pool    *ipam.IPPool
	cidr    string
	network *net.IPNet
}

func newTestDriver() *testDriver {
	t := &testDriver{}

	BeforeEach(func() {
		t.cidr = "169.254.1.0/24"
	})

	JustBeforeEach(func() {
		var err error

		_, t.network, err = net.ParseCIDR(t.cidr)
		Expect(err).To(Succeed())

		t.pool, err = ipam.NewIPPool(t.cidr)
		Expect(err).To(Succeed())
	})

	return t
}

func (t *testDriver) allocate(num int) []string {
	ips, err := t.pool.Allocate(num)
	Expect(err).To(Succeed())
	Expect(ips).To(HaveLen(num))

	return ips
}
