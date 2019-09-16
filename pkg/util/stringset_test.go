package util

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("StringSet", func() {
	Describe("Unit tests for NewStringSet", func() {
		Context("When subnetList contains a specified string", func() {
			It("Should return true", func() {
				subnetList := NewStringSet()
				subnetList.Add("192.168.1.0/24")
				subnetList.Add("192.168.2.0/24")
				Expect(subnetList.Contains("192.168.2.0/24")).Should(Equal(true))
			})
		})
		Context("When subnetList does not contain a specified string", func() {
			It("Should return false", func() {
				subnetList := NewStringSet()
				subnetList.Add("192.168.1.0/24")
				subnetList.Add("192.168.2.0/24")
				Expect(subnetList.Contains("192.168.3.0/24")).Should(Equal(false))
			})
		})
		Context("When subnetList already has an entry", func() {
			It("Should not append to the list", func() {
				subnetList := NewStringSet()
				subnetList.Add("192.168.1.0/24")
				subnetList.Add("192.168.2.0/24")
				subnetList.Add("192.168.3.0/24")
				subnetList.Add("192.168.2.0/24")
				Expect(subnetList.Size()).Should(Equal(3))
			})
		})
		Context("When an entry is deleted from subnetList", func() {
			It("Should be removed from the list", func() {
				subnetList := NewStringSet()
				subnetList.Add("192.168.1.0/24")
				subnetList.Add("192.168.2.0/24")
				subnetList.Add("192.168.3.0/24")
				subnetList.Delete("192.168.2.0/24")
				Expect(subnetList.Contains("192.168.2.0/24")).Should(Equal(false))
				Expect(subnetList.Size()).Should(Equal(2))
			})
		})
	})
})

func TestStringSet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "StringSet Suite")
}
