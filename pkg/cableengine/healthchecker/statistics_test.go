package healthchecker

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("", func() {
	Context("Test Statistics", testStatistics)
})

const (
	testMinRTT     = 404351
	testMaxRTT     = 1048263
	testLastRTT    = 1044609
	testNewMinRTT  = 404300
	testNewMaxRTT  = 1048264
	testNewLastRTT = 609555
)

func testStatistics() {
	size := 10
	statistics := &Statistics{
		size:         uint64(size),
		previousRtts: make([]uint64, size),
	}

	sampleSpace := [10]uint64{testMinRTT, 490406, 530333, 609556, 609650, 685106, 726265, 785707, testMaxRTT, testLastRTT}
	expectedMean := 693424
	expectedSD := 205994

	for _, v := range sampleSpace {
		statistics.updateStatistics(v)
	}

	Expect(statistics.maxRtt).To(Equal(uint64(testMaxRTT)))
	Expect(statistics.minRtt).To(Equal(uint64(testMinRTT)))
	Expect(statistics.lastRtt).To(Equal(uint64(testLastRTT)))
	Expect(statistics.mean).To(Equal(uint64(expectedMean)))
	Expect(statistics.stdDev).To(Equal(uint64(expectedSD)))

	statistics.updateStatistics(testNewMinRTT)
	statistics.updateStatistics(testNewMaxRTT)
	statistics.updateStatistics(testNewLastRTT)

	newExpectedMean := 830998
	newExpectedSD := 272450

	Expect(statistics.maxRtt).To(Equal(uint64(testNewMaxRTT)))
	Expect(statistics.minRtt).To(Equal(uint64(testNewMinRTT)))
	Expect(statistics.lastRtt).To(Equal(uint64(testNewLastRTT)))
	Expect(statistics.mean).To(Equal(uint64(newExpectedMean)))
	Expect(statistics.stdDev).To(Equal(uint64(newExpectedSD)))
}

func TestSyncer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Statistics Suite")
}
