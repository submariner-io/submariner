package ipam

import (
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
	service1 = "default/service1"
	pod1 = "default/pod1"
	pod2 = "default/pod2"
)
func testPoolCreation() {
	When("CIDR is 169.254.0.0", func() {
		It("should fail to create IP pool with errors", func() {
			pool, err := NewIpPool("169.254.0.0")
			Expect(err).To(HaveOccurred())
			Expect(pool).To(BeNil())
		})
	})
	When("CIDR is 169.254.0.0/33", func() {
		It("should fail to create IP pool with errors", func() {
			pool, err := NewIpPool("169.254.0.0/33")
			Expect(err).To(HaveOccurred())
			Expect(pool).To(BeNil())
		})
	})
	When("CIDR Prefix is /32", func() {
		It("should fail to create IP pool with errors", func() {
			pool, err := NewIpPool("169.254.1.0/32")
			Expect(err).To(HaveOccurred())
			Expect(pool).To(BeNil())
		})
	})
	When("CIDR Prefix is /31", func() {
		It("should fail to create IP pool with errors", func() {
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
		It("should create IP pool of size 254", func() {
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
		pool,_ := NewIpPool("169.254.1.1/30")
		available := len(pool.available)
		allocated := len(pool.allocated)
		service1Ip, err := pool.Allocate(service1)

		It("should not throw any error", func() {
			Expect(err).NotTo(HaveOccurred())
		})
		It("should return an IP", func() {
			Expect(service1Ip).NotTo(BeNil())
		})

		It("should decrement available IPs pool size by 1", func() {
			Expect(available - len(pool.available)).Should(Equal(1))
		})
		It("should increment allocated IPs pool size by 1", func() {
			Expect(len(pool.allocated) - allocated).Should(Equal(1))
		})
		It("should not decrement available IPs pool size if same key is reused", func() {
			pool.Allocate(service1)
			Expect(pool.size - len(pool.available)).Should(Equal(1))
		})
		It("should not increment allocated IPs pool size if same key is reused", func() {
			pool.Allocate(service1)
			Expect(len(pool.allocated)).Should(Equal(1))
		})
		It("should mark allocated IP as not available", func() {
			Expect(pool.IsAvailable(service1Ip)).To(BeFalse())
		})
	})

	When("Specific IP is Requested", func() {
		pool,_ := NewIpPool("169.254.1.1/30")
		service1Ip, err := pool.RequestIp(service1, requestIp1)
		It("should not throw any error", func() {
			Expect(err).NotTo(HaveOccurred())
		})
		It("should return IP requested when available", func() {
			Expect(service1Ip).Should(Equal(requestIp1))
		})
		It("should return different IP when requested IP is not available", func() {
			requestedIp2, _ := pool.RequestIp(pod1, requestIp1)
			Expect(requestedIp2).ShouldNot(Equal(requestIp1))
		})
		It("should return same IP when same key is reused", func() {
			requestedIp2, _ := pool.RequestIp(service1, requestIp1)
			Expect(requestedIp2).Should(Equal(requestIp1))
		})
	})

	When("All IPs are allocated", func() {
		pool,_ := NewIpPool("169.254.1.1/30")
		service1Ip, _ := pool.Allocate(service1)
		pool.Allocate(pod1)
		It("IsFull should return true", func() {
			Expect(pool.IsFull()).To(BeTrue())
		})
		It("requesting any IP should return error", func() {
			pod2Ip, err := pool.Allocate(pod2)
			Expect(err).To(HaveOccurred())
			Expect(pod2Ip).Should(Equal(""))
		})
		It("should return same IP when same key/Ip is requested", func() {
			requestedIp, _ := pool.RequestIp(service1, service1Ip)
			Expect(requestedIp).Should(Equal(requestIp1))
		})
		It("should return error when different key/Ip is requested", func() {
			requestedIp, err := pool.RequestIp(pod1, requestIp1)
			Expect(err).To(HaveOccurred())
			Expect(requestedIp).Should(Equal(""))
		})
	})

}

func testIpRelease() {
	When("Allocated IP is Released", func() {
		pool,_ := NewIpPool("169.254.1.1/30")
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
	When("UnAllocated IP is Released", func() {
		pool,_ := NewIpPool("169.254.1.1/30")
		pool.Allocate(service1)
		available := len(pool.available)
		allocated := len(pool.allocated)
		releasedIp := pool.Release(pod1)
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
