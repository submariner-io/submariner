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
package ipam

import (
	"net"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Globalnet IP Pool", func() {
	Context("Pool Creation", testPoolCreation)
	Context("IP Allocation", testIpAllocation)
	Context("IP Release", testIpRelease)
})

const (
	requestIp1 = "169.254.1.1"
	service1   = "default/service1"
	pod1       = "default/pod1"
	pod2       = "default/pod2"
	testCidr   = "169.254.1.1/30"
)

func testPoolCreation() {
	When("CIDR is missing the prefix", func() {
		It("should fail to create the IP pool with an error", func() {
			pool, err := NewIpPool("169.254.0.0")
			Expect(err).To(HaveOccurred())
			Expect(pool).To(BeNil())
		})
	})
	When("CIDR prefix is /33", func() {
		It("should fail to create IP pool with an error", func() {
			pool, err := NewIpPool("169.254.0.0/33")
			Expect(err).To(HaveOccurred())
			Expect(pool).To(BeNil())
		})
	})
	When("CIDR Prefix is /32", func() {
		It("should fail to create IP pool with an error", func() {
			pool, err := NewIpPool("169.254.1.0/32")
			Expect(err).To(HaveOccurred())
			Expect(pool).To(BeNil())
		})
	})
	When("CIDR Prefix is /31", func() {
		It("should fail to create IP pool with an error", func() {
			pool, err := NewIpPool("169.254.1.0/31")
			Expect(err).To(HaveOccurred())
			Expect(pool).To(BeNil())
		})
	})
	When("CIDR Prefix is /30", func() {
		It("should create IP pool of size 2", func() {
			pool, err := NewIpPool("169.254.1.0/30")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(pool.available)).Should(Equal(2))
		})
	})
	When("CIDR Prefix is /24", func() {
		It("should create IP pool of size 254", func() {
			pool, err := NewIpPool("169.254.1.0/24")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(pool.available)).Should(Equal(254))
		})
	})
	When("CIDR Prefix is /16", func() {
		It("should create an IP pool of size 65534", func() {
			pool, err := NewIpPool("169.254.1.0/16")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(pool.available)).Should(Equal(65534))
		})
	})
	When("Any pool is created", func() {
		It("should have allocated pool size of 0", func() {
			pool, err := NewIpPool("169.254.1.0/16")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(pool.allocated)).Should(Equal(0))
		})
	})
}

func testIpAllocation() {
	When("any IP is requested", testAnyIpRequested)
	When("a specific IP is Requested", testSpecificIpRequested)
	When("all IPs are allocated", testAllIpsAllocated)
}

func testAnyIpRequested() {
	var (
		pool        *IpPool
		available   int
		allocated   int
		allocatedIp string
	)

	BeforeEach(func() {
		var err error
		pool, err = NewIpPool(testCidr)
		Expect(err).NotTo(HaveOccurred())

		available = len(pool.available)
		allocated = len(pool.allocated)

		allocatedIp, err = pool.Allocate(service1)
		Expect(err).NotTo(HaveOccurred())
		Expect(allocatedIp).NotTo(BeNil())
	})

	It("should return a valid IP for the CIDR range", func() {
		_, ipnet, err := net.ParseCIDR(testCidr)
		Expect(err).NotTo(HaveOccurred())
		Expect(ipnet.Contains(net.ParseIP(allocatedIp))).Should(BeTrue())
	})

	It("should decrement the available IP Pool size by 1", func() {
		Expect(available - len(pool.available)).Should(Equal(1))
	})

	It("should increment the allocated IP Pool size by 1", func() {
		Expect(len(pool.allocated) - allocated).Should(Equal(1))
	})

	When("attempting to re-allocate a previously allocated key", func() {
		var resultIp string
		BeforeEach(func() {
			var err error
			resultIp, err = pool.Allocate(service1)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return the previously allocated IP", func() {
			Expect(resultIp).Should(Equal(allocatedIp))
		})

		It("should not decrement the available IP Pool size", func() {
			Expect(pool.size - len(pool.available)).Should(Equal(1))
		})

		It("should not increment the allocated IP Pool size", func() {
			Expect(len(pool.allocated) - allocated).Should(Equal(1))
		})
	})

	It("should mark the allocated IP as unavailable", func() {
		Expect(pool.IsAvailable(allocatedIp)).To(BeFalse())
	})
}

func testSpecificIpRequested() {
	var (
		pool        *IpPool
		requestedIp string
	)

	BeforeEach(func() {
		var err error
		pool, err = NewIpPool(testCidr)
		Expect(err).NotTo(HaveOccurred())

		requestedIp, err = pool.RequestIp(service1, requestIp1)
		Expect(err).NotTo(HaveOccurred())
		Expect(requestedIp).NotTo(BeNil())
	})

	It("should return the requested IP", func() {
		Expect(requestedIp).Should(Equal(requestIp1))
	})

	When("an unavailable IP is requested for an unallocated key", func() {
		It("should allocate and return a different IP", func() {
			resultIp, err := pool.RequestIp(pod1, requestIp1)
			Expect(err).NotTo(HaveOccurred())
			Expect(resultIp).ShouldNot(Equal(requestIp1))
		})
	})

	When("an IP is requested that matches that of a previously allocated key", func() {
		It("should return the same IP", func() {
			resultIp, err := pool.RequestIp(service1, requestIp1)
			Expect(err).NotTo(HaveOccurred())
			Expect(resultIp).Should(Equal(requestIp1))
		})
	})

	When("an unavailable IP is requested that does not match that of a previously allocated key", func() {
		It("should return the previously allocated IP", func() {
			unavailableIp, err := pool.Allocate(pod1)
			Expect(err).NotTo(HaveOccurred())

			resultIp, err := pool.RequestIp(service1, unavailableIp)
			Expect(err).NotTo(HaveOccurred())
			Expect(resultIp).Should(Equal(requestedIp))
		})
	})
}

func testAllIpsAllocated() {
	var (
		pool       *IpPool
		service1Ip string
		pod1Ip     string
	)

	BeforeEach(func() {
		var err error
		pool, err = NewIpPool(testCidr)
		Expect(err).NotTo(HaveOccurred())

		service1Ip, err = pool.Allocate(service1)
		Expect(err).NotTo(HaveOccurred())

		pod1Ip, err = pool.Allocate(pod1)
		Expect(err).NotTo(HaveOccurred())
	})

	When("any IP is requested for an unallocated key", func() {
		It("should return an error", func() {
			_, err := pool.Allocate(pod2)
			Expect(err).To(HaveOccurred())
		})
	})

	When("an IP is requested that matches that of a previously allocated key", func() {
		It("should return the same IP", func() {
			resultIp, err := pool.RequestIp(service1, service1Ip)
			Expect(err).NotTo(HaveOccurred())
			Expect(resultIp).Should(Equal(service1Ip))
		})
	})

	When("an IP is requested that does not match that of a previously allocated key", func() {
		It("should return the previously allocated IP", func() {
			resultIp, err := pool.RequestIp(pod1, service1Ip)
			Expect(err).NotTo(HaveOccurred())
			Expect(resultIp).Should(Equal(pod1Ip))
		})
	})
}

func testIpRelease() {
	var (
		pool        *IpPool
		available   int
		allocated   int
		allocatedIp string
	)

	BeforeEach(func() {
		var err error
		pool, err = NewIpPool(testCidr)
		Expect(err).NotTo(HaveOccurred())

		allocatedIp, err = pool.Allocate(service1)
		Expect(err).NotTo(HaveOccurred())

		available = len(pool.available)
		allocated = len(pool.allocated)
	})

	When("an allocated IP is released", func() {
		var releasedIp string

		BeforeEach(func() {
			releasedIp = pool.Release(service1)
		})

		It("should return the allocated IP", func() {
			Expect(releasedIp).Should(Equal(allocatedIp))
		})

		It("should increment the available IP count by 1", func() {
			Expect(len(pool.available) - available).Should(Equal(1))
		})

		It("should decrement the allocated IP count by 1", func() {
			Expect(allocated - len(pool.allocated)).Should(Equal(1))
		})

		It("should become available for re-allocation", func() {
			Expect(pool.IsAvailable(releasedIp)).To(BeTrue())
		})
	})

	When("an unallocated IP is released", func() {
		var releasedIp string

		BeforeEach(func() {
			releasedIp = pool.Release(pod1)
		})

		It("should return an empty IP", func() {
			Expect(releasedIp).Should(Equal(""))
		})

		It("should not increment the available IP count", func() {
			Expect(len(pool.available)).Should(Equal(available))
		})

		It("should not decrement the allocated IP count", func() {
			Expect(len(pool.allocated)).Should(Equal(allocated))
		})
	})
}

func TestIpPool(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Globalnet/IPAM/IpPool Suite")
}
