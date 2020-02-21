package util_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/pkg/util"
)

var _ = Describe("StringSet", func() {
	var set *util.StringSet

	BeforeEach(func() {
		set = util.NewStringSet()
		Expect(set.Add("192.168.1.0/24")).To(BeTrue())
		Expect(set.Add("192.168.2.0/24")).To(BeTrue())
	})

	It("should return the correct size", func() {
		Expect(set.Size()).To(Equal(2))
	})

	When("it contains a specified string", func() {
		It("should return true", func() {
			Expect(set.Contains("192.168.1.0/24")).To(BeTrue())
			Expect(set.Contains("192.168.2.0/24")).To(BeTrue())
		})
	})

	When("it does not contain a specified string", func() {
		It("should return false", func() {
			Expect(set.Contains("192.168.3.0/24")).To(BeFalse())
		})
	})

	When("adding a string that already exists", func() {
		It("should not add it again", func() {
			Expect(set.Add("192.168.1.0/24")).To(BeFalse())
			Expect(set.Size()).To(Equal(2))
			Expect(set.Contains("192.168.1.0/24")).To(BeTrue())
		})
	})

	When("a string is deleted", func() {
		It("should no longer be observed in the set", func() {
			set.Delete("192.168.2.0/24")
			Expect(set.Contains("192.168.2.0/24")).To(BeFalse())
			Expect(set.Size()).To(Equal(1))
		})
	})

	When("a string is re-added", func() {
		It("should be observed in the set", func() {
			set.Delete("192.168.2.0/24")
			set.Add("192.168.2.0/24")
			Expect(set.Contains("192.168.2.0/24")).To(BeTrue())
			Expect(set.Size()).To(Equal(2))
		})
	})

	It("should return the correct elements", func() {
		containsElements(set.Elements(), "192.168.1.0/24", "192.168.2.0/24")

		set.Add("192.168.3.0/24")
		containsElements(set.Elements(), "192.168.1.0/24", "192.168.2.0/24", "192.168.3.0/24")

		set.Delete("192.168.1.0/24")
		set.Delete("192.168.3.0/24")
		containsElements(set.Elements(), "192.168.2.0/24")

		containsElements(util.NewStringSet().Elements())
	})
})

func containsElements(actual []string, exp ...string) {
	for _, s := range exp {
		Expect(actual).To(ContainElement(s))
	}

	Expect(actual).To(HaveLen(len(exp)))
}
