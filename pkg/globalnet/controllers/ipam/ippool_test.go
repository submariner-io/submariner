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
package ipam

import (
	"fmt"
	"net"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Globalnet IP Pool", func() {
	Context("Pool Creation", testPoolCreation)
	Context("IP Allocation", testIPAllocation)
	Context("IP Release", testIPRelease)
	Context("Block IPs", testBlockIPs)
})

const (
	requestIP1    = "169.254.1.1"
	service1      = "default/service1"
	pod1          = "default/pod1"
	pod2          = "default/pod2"
	testCidr      = "169.254.1.1/30"
	testBlockCidr = "169.254.1.1/28"
	blockKey1     = "block1"
	blockSize1    = 4
	blockKey2     = "block2"
	blockSize2    = 8
)

func testPoolCreation() {
	When("CIDR is missing the prefix", func() {
		It("should fail to create the IP pool with an error", func() {
			pool, err := NewIPPool("169.254.0.0")
			Expect(err).To(HaveOccurred())
			Expect(pool).To(BeNil())
		})
	})
	When("CIDR prefix is /33", func() {
		It("should fail to create IP pool with an error", func() {
			pool, err := NewIPPool("169.254.0.0/33")
			Expect(err).To(HaveOccurred())
			Expect(pool).To(BeNil())
		})
	})
	When("CIDR Prefix is /32", func() {
		It("should fail to create IP pool with an error", func() {
			pool, err := NewIPPool("169.254.1.0/32")
			Expect(err).To(HaveOccurred())
			Expect(pool).To(BeNil())
		})
	})
	When("CIDR Prefix is /31", func() {
		It("should fail to create IP pool with an error", func() {
			pool, err := NewIPPool("169.254.1.0/31")
			Expect(err).To(HaveOccurred())
			Expect(pool).To(BeNil())
		})
	})
	When("CIDR Prefix is /30", func() {
		It("should create IP pool of size 2", func() {
			pool, err := NewIPPool("169.254.1.0/30")
			Expect(err).NotTo(HaveOccurred())
			Expect(pool.available.Size()).Should(Equal(2))
		})
	})
	When("CIDR Prefix is /24", func() {
		It("should create IP pool of size 254", func() {
			pool, err := NewIPPool("169.254.1.0/24")
			Expect(err).NotTo(HaveOccurred())
			Expect(pool.available.Size()).Should(Equal(254))
		})
	})
	When("CIDR Prefix is /16", func() {
		It("should create an IP pool of size 65534", func() {
			pool, err := NewIPPool("169.254.1.0/16")
			Expect(err).NotTo(HaveOccurred())
			Expect(pool.available.Size()).Should(Equal(65534))
		})
	})
	When("Any pool is created", func() {
		It("should have allocated pool size of 0", func() {
			pool, err := NewIPPool("169.254.1.0/16")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(pool.allocated)).Should(Equal(0))
		})
	})
}

func testIPAllocation() {
	When("any IP is requested", testAnyIPRequested)
	When("a specific IP is Requested", testSpecificIPRequested)
	When("all IPs are allocated", testAllIpsAllocated)
}

func testAnyIPRequested() {
	var (
		pool        *IPPool
		available   int
		allocated   int
		allocatedIP string
	)

	BeforeEach(func() {
		var err error
		pool, err = NewIPPool(testCidr)
		Expect(err).NotTo(HaveOccurred())

		available = pool.available.Size()
		allocated = len(pool.allocated)

		allocatedIP, err = pool.Allocate(service1)
		Expect(err).NotTo(HaveOccurred())
		Expect(allocatedIP).NotTo(BeNil())
	})

	It("should return a valid IP for the CIDR range", func() {
		_, ipnet, err := net.ParseCIDR(testCidr)
		Expect(err).NotTo(HaveOccurred())
		Expect(ipnet.Contains(net.ParseIP(allocatedIP))).Should(BeTrue())
	})

	It("should decrement the available IP Pool size by 1", func() {
		Expect(available - pool.available.Size()).Should(Equal(1))
	})

	It("should increment the allocated IP Pool size by 1", func() {
		Expect(len(pool.allocated) - allocated).Should(Equal(1))
	})

	When("attempting to re-allocate a previously allocated key", func() {
		var resultIP string
		BeforeEach(func() {
			var err error
			resultIP, err = pool.Allocate(service1)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return the previously allocated IP", func() {
			Expect(resultIP).Should(Equal(allocatedIP))
		})

		It("should not decrement the available IP Pool size", func() {
			Expect(pool.size - pool.available.Size()).Should(Equal(1))
		})

		It("should not increment the allocated IP Pool size", func() {
			Expect(len(pool.allocated) - allocated).Should(Equal(1))
		})
	})

	It("should mark the allocated IP as unavailable", func() {
		Expect(pool.IsAvailable(allocatedIP)).To(BeFalse())
	})
}

func testSpecificIPRequested() {
	var (
		pool        *IPPool
		requestedIP string
	)

	BeforeEach(func() {
		var err error
		pool, err = NewIPPool(testCidr)
		Expect(err).NotTo(HaveOccurred())

		requestedIP, err = pool.RequestIP(service1, requestIP1)
		Expect(err).NotTo(HaveOccurred())
		Expect(requestedIP).NotTo(BeNil())
	})

	It("should return the requested IP", func() {
		Expect(requestedIP).Should(Equal(requestIP1))
	})

	When("an unavailable IP is requested for an unallocated key", func() {
		It("should allocate and return a different IP", func() {
			resultIP, err := pool.RequestIP(pod1, requestIP1)
			Expect(err).NotTo(HaveOccurred())
			Expect(resultIP).ShouldNot(Equal(requestIP1))
		})
	})

	When("an IP is requested that matches that of a previously allocated key", func() {
		It("should return the same IP", func() {
			resultIP, err := pool.RequestIP(service1, requestIP1)
			Expect(err).NotTo(HaveOccurred())
			Expect(resultIP).Should(Equal(requestIP1))
		})
	})

	When("an unavailable IP is requested that does not match that of a previously allocated key", func() {
		It("should return the previously allocated IP", func() {
			unavailableIP, err := pool.Allocate(pod1)
			Expect(err).NotTo(HaveOccurred())

			resultIP, err := pool.RequestIP(service1, unavailableIP)
			Expect(err).NotTo(HaveOccurred())
			Expect(resultIP).Should(Equal(requestedIP))
		})
	})
}

func testAllIpsAllocated() {
	var (
		pool       *IPPool
		service1Ip string
		pod1Ip     string
	)

	BeforeEach(func() {
		var err error
		pool, err = NewIPPool(testCidr)
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
			resultIP, err := pool.RequestIP(service1, service1Ip)
			Expect(err).NotTo(HaveOccurred())
			Expect(resultIP).Should(Equal(service1Ip))
		})
	})

	When("an IP is requested that does not match that of a previously allocated key", func() {
		It("should return the previously allocated IP", func() {
			resultIP, err := pool.RequestIP(pod1, service1Ip)
			Expect(err).NotTo(HaveOccurred())
			Expect(resultIP).Should(Equal(pod1Ip))
		})
	})
}

func testIPRelease() {
	var (
		pool        *IPPool
		available   int
		allocated   int
		allocatedIP string
	)

	BeforeEach(func() {
		var err error
		pool, err = NewIPPool(testCidr)
		Expect(err).NotTo(HaveOccurred())

		allocatedIP, err = pool.Allocate(service1)
		Expect(err).NotTo(HaveOccurred())

		available = pool.available.Size()
		allocated = len(pool.allocated)
	})

	When("an allocated IP is released", func() {
		var releasedIP string

		BeforeEach(func() {
			releasedIP = pool.Release(service1)
		})

		It("should return the allocated IP", func() {
			Expect(releasedIP).Should(Equal(allocatedIP))
		})

		It("should increment the available IP count by 1", func() {
			Expect(pool.available.Size() - available).Should(Equal(1))
		})

		It("should decrement the allocated IP count by 1", func() {
			Expect(allocated - len(pool.allocated)).Should(Equal(1))
		})

		It("should become available for re-allocation", func() {
			Expect(pool.IsAvailable(releasedIP)).To(BeTrue())
		})
	})

	When("an unallocated IP is released", func() {
		var releasedIP string

		BeforeEach(func() {
			releasedIP = pool.Release(pod1)
		})

		It("should return an empty IP", func() {
			Expect(releasedIP).Should(Equal(""))
		})

		It("should not increment the available IP count", func() {
			Expect(pool.available.Size()).Should(Equal(available))
		})

		It("should not decrement the allocated IP count", func() {
			Expect(len(pool.allocated)).Should(Equal(allocated))
		})
	})
}

func testBlockIPs() {
	When("a block of IPs are allocated", testBlockIPsAllocated)
	When("a small contiguous block of IPs is available", testContiguousBlock)
}

func testBlockIPsAllocated() {
	var (
		pool      *IPPool
		available int
		allocated int
		ips       []string
	)

	BeforeEach(func() {
		var err error
		pool, err = NewIPPool(testBlockCidr)
		Expect(err).NotTo(HaveOccurred())

		available = pool.available.Size()
		allocated = len(pool.allocated)

		ips, err = pool.AllocateIPBlock(blockKey1, blockSize1)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(ips)).To(Equal(blockSize1))
	})

	It("should decrement the available IP Pool size by blocksize", func() {
		Expect(available - pool.available.Size()).Should(Equal(blockSize1))
	})

	It("should increment the allocated IP Pool size by 1", func() {
		Expect(len(pool.allocated) - allocated).Should(Equal(blockSize1))
	})

	It("Should return a contiguous set of IPs", func() {
		Expect(isContiguous(ips)).Should(BeTrue())
	})

	When("any IP is requested", func() {
		var anyIP string
		BeforeEach(func() {
			var err error

			anyIP, err = pool.Allocate(service1)
			Expect(err).NotTo(HaveOccurred())
		})

		It("it should return an IP", func() {
			Expect(anyIP).NotTo(BeNil())
		})
		It("returned IP should not be from block of IPs allocated", func() {
			Expect(isIPInList(ips, anyIP)).Should(BeFalse())
		})
	})

	When("an IP from block is requested", func() {
		It("it should return different IP", func() {
			ip, _ := pool.RequestIP(service1, ips[0])
			Expect(ip).To(Not(Equal(ips[0])))
		})
	})

	When("another block of IPs is requested", func() {
		var (
			ips2 []string
		)
		BeforeEach(func() {
			var err error

			ips2, err = pool.AllocateIPBlock(blockKey2, blockSize2)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should decrement the available IP Pool size by block size", func() {
			Expect(available - pool.available.Size()).Should(Equal(blockSize1 + blockSize2))
		})

		It("should increment the allocated IP Pool size by block size", func() {
			Expect(len(pool.allocated) - allocated).Should(Equal(blockSize1 + blockSize2))
		})

		It("should return a contiguous set of IPs", func() {
			Expect(isContiguous(ips2)).Should(BeTrue())
		})

		It("should not overlap with previously allocated block", func() {
			Expect(isOverlap(ips, ips2)).Should(BeFalse())
		})
	})

	When("block of IPs is released", func() {
		BeforeEach(func() {
			available = pool.available.Size()
			allocated = len(pool.allocated)
			pool.ReleaseIPBlock(blockKey1, blockSize1)
		})

		It("should increment the available IP Pool size by block size", func() {
			Expect(pool.available.Size() - available).Should(Equal(blockSize1))
		})

		It("should decrement the allocated IP Pool size by 1", func() {
			Expect(allocated - len(pool.allocated)).Should(Equal(blockSize1))
		})
	})
}

func testContiguousBlock() {
	const svcKey = "svc"
	var (
		pool *IPPool
	)

	BeforeEach(func() {
		var err error
		pool, err = NewIPPool(testBlockCidr)
		Expect(err).NotTo(HaveOccurred())

		// Allocate IPs so that no contiguous block is more than blockSize1-1
		for i := 0; i < pool.size; i++ {
			if i%blockSize1 == 0 {
				_, err = pool.RequestIP(fmt.Sprintf("%s-%d", svcKey, i), fmt.Sprintf("169.254.1.%d", i+1))
				Expect(err).NotTo(HaveOccurred())
			}
		}
	})

	When("any IP is requested", func() {
		var anyIP string
		BeforeEach(func() {
			var err error

			anyIP, err = pool.Allocate(service1)
			Expect(err).NotTo(HaveOccurred())
		})

		It("it should return an IP", func() {
			Expect(anyIP).NotTo(BeNil())
		})
	})

	When("requesting IP Block of size greater than available contiguous block", func() {
		It("it should fail to allocate", func() {
			ips, err := pool.AllocateIPBlock(blockKey1, blockSize1)
			Expect(ips).To(BeNil())
			Expect(err).To(HaveOccurred())
		})
	})

	When("requesting IP Block of size lesser than or equal to available contiguous block", func() {
		It("it should allocate", func() {
			ips, err := pool.AllocateIPBlock(blockKey1, blockSize1-1)
			Expect(err).ToNot(HaveOccurred())
			Expect(isContiguous(ips)).To(BeTrue())
		})
	})
}

func isContiguous(ips []string) bool {
	size := len(ips)
	for prev, curr := 0, 0; curr < size; prev, curr = curr, curr+1 {
		if curr == 0 {
			continue
		}

		if StringIPToInt(ips[prev])+1 != StringIPToInt(ips[curr]) {
			return false
		}
	}

	return true
}

func isIPInList(ips []string, ip string) bool {
	for _, v := range ips {
		if v == ip {
			return true
		}
	}

	return false
}

func isOverlap(list1, list2 []string) bool {
	result := make(map[string]bool)
	for _, v := range list1 {
		result[v] = true
	}
	for _, v := range list2 {
		if result[v] {
			return true
		}
	}

	return false
}
