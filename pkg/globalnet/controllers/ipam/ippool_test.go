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
	When("Any IP is requested", func() {
		pool, _ := NewIpPool(testCidr)
		available := len(pool.available)
		allocated := len(pool.allocated)
		service1Ip, err := pool.Allocate(service1)

		It("should not return an error", func() {
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return a valid IP for CIDR range", func() {
			Expect(service1Ip).NotTo(BeNil())
			_, ipnet, _ := net.ParseCIDR(testCidr)
			Expect(ipnet.Contains(net.ParseIP(service1Ip))).Should(BeTrue())
		})

		It("should decrement available IP Pool size by 1", func() {
			Expect(available - len(pool.available)).Should(Equal(1))
		})

		It("should increment allocated IP Pool size by 1", func() {
			Expect(len(pool.allocated) - allocated).Should(Equal(1))
		})

		It("should not decrement available IP Pool size if same key is reused", func() {
			_, err := pool.Allocate(service1)
			Expect(err).NotTo(HaveOccurred())
			Expect(pool.size - len(pool.available)).Should(Equal(1))
		})

		It("should not increment allocated IP Pool size if same key is reused", func() {
			_, err := pool.Allocate(service1)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(pool.allocated)).Should(Equal(1))
		})

		It("should mark allocated IP as not available", func() {
			Expect(pool.IsAvailable(service1Ip)).To(BeFalse())
		})

		It("IsFull should return false", func() {
			Expect(pool.IsFull()).To(BeFalse())
		})
	})

	When("Specific IP is Requested", func() {
		pool, _ := NewIpPool(testCidr)
		service1Ip, err := pool.RequestIp(service1, requestIp1)

		It("should not return an error", func() {
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return the requested IP", func() {
			Expect(service1Ip).Should(Equal(requestIp1))
		})

		When("the same IP but different key is requested", func() {
			requestedIp2, err := pool.RequestIp(pod1, requestIp1)

			It("should return a different IP", func() {
				Expect(err).NotTo(HaveOccurred())
				Expect(requestedIp2).ShouldNot(Equal(requestIp1))
			})
		})

		When("the same IP and key is requested", func() {
			requestedIp2, err := pool.RequestIp(service1, requestIp1)

			It("should return same IP", func() {
				Expect(err).NotTo(HaveOccurred())
				Expect(requestedIp2).Should(Equal(requestIp1))
			})
		})
	})

	When("All IPs are allocated", func() {
		pool, _ := NewIpPool(testCidr)
		service1Ip, err := pool.Allocate(service1)
		_, err = pool.Allocate(pod1)

		It("should not return an error", func() {
			Expect(err).NotTo(HaveOccurred())
		})

		When("IsFull is called", func() {
			It("should return true", func() {
				Expect(pool.IsFull()).To(BeTrue())
			})
		})

		When("any IP is requested for an unallocated key", func() {
			pod2Ip, err := pool.Allocate(pod2)

			It("should return an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(pod2Ip).Should(Equal(""))
			})
		})

		When("the same IP is requested for an allocated key", func() {
			requestedIp, err := pool.RequestIp(service1, service1Ip)

			It("should not return an error", func() {
				Expect(err).NotTo(HaveOccurred())
			})

			It("should return same IP", func() {
				Expect(requestedIp).Should(Equal(service1Ip))
			})
		})

		When("a different IP is requested for an allocated key", func() {
			requestedIp, err := pool.RequestIp(pod1, requestIp1)

			It("should return an error", func() {
				Expect(err).To(HaveOccurred())
				Expect(requestedIp).Should(Equal(""))
			})
		})
	})
}

func testIpRelease() {
	When("Allocated IP is Released", func() {
		pool, _ := NewIpPool(testCidr)
		service1Ip, _ := pool.Allocate(service1)
		available := len(pool.available)
		allocated := len(pool.allocated)
		releasedIp := pool.Release(service1)

		It("should return IP released", func() {
			Expect(releasedIp).Should(Equal(service1Ip))
		})

		It("should increment available IP count by 1", func() {
			Expect(len(pool.available) - available).Should(Equal(1))
		})

		It("should decrement allocated IP count by 1", func() {
			Expect(allocated - len(pool.allocated)).Should(Equal(1))
		})

		It("should become available for allocation", func() {
			Expect(pool.IsAvailable(releasedIp)).To(BeTrue())
		})
	})

	When("Unallocated IP is Released", func() {
		pool, _ := NewIpPool(testCidr)
		_, err := pool.Allocate(service1)
		available := len(pool.available)
		allocated := len(pool.allocated)
		releasedIp := pool.Release(pod1)

		It("should not return an error", func() {
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return empty string", func() {
			Expect(releasedIp).Should(Equal(""))
		})

		It("should not increment available IP count", func() {
			Expect(len(pool.available)).Should(Equal(available))
		})

		It("should not decrement allocated IP count", func() {
			Expect(len(pool.allocated)).Should(Equal(allocated))
		})
	})
}

func TestIpPool(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Globalnet/IPAM/IpPool Suite")
}
