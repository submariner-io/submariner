package healthchecker

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("", func() {
	Context("Test Statistics", testStatistics)
})

func testStatistics() {
	size := 10
	statistics := &Statistics{
		size:         uint64(size),
		previousRtts: make([]uint64, size),
	}

	sampleSpace := [10]uint64{404351, 490406, 530333, 609556, 609650, 685106, 726265, 785707, 1044609, 1048263}
	for _, v := range sampleSpace {
		statistics.updateStatistics(v)
	}

	Expect(statistics.maxRtt).To(Equal(uint64(1048263)))
	Expect(statistics.minRtt).To(Equal(uint64(404351)))
	Expect(statistics.lastRtt).To(Equal(uint64(1048263)))
	Expect(statistics.mean).To(Equal(uint64(693424)))
	Expect(statistics.stdDev).To(Equal(uint64(205994)))

	statistics.updateStatistics(404300)
	statistics.updateStatistics(1048264)

	Expect(statistics.maxRtt).To(Equal(uint64(1048264)))
	Expect(statistics.minRtt).To(Equal(uint64(404300)))
	Expect(statistics.lastRtt).To(Equal(uint64(1048264)))
	Expect(statistics.mean).To(Equal(uint64(886359)))
	Expect(statistics.stdDev).To(Equal(uint64(278320)))
}

func TestSyncer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Statistics Suite")
}
