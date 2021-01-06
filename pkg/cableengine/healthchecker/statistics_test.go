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
package healthchecker

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Statistics", func() {
	const (
		testMinRTT     = 404351
		testMaxRTT     = 1048263
		testLastRTT    = 1044609
		testNewMinRTT  = 404300
		testNewMaxRTT  = 1048264
		testNewLastRTT = 609555
	)

	When("update is called with a sample space", func() {
		It("should correctly compute the statistics", func() {
			size := 10
			statistics := &statistics{
				size:         uint64(size),
				previousRtts: make([]uint64, size),
			}

			sampleSpace := [10]uint64{testMinRTT, 490406, 530333, 609556, 609650, 685106, 726265, 785707, testMaxRTT, testLastRTT}
			expectedMean := 693424
			expectedSD := 205994

			for _, v := range sampleSpace {
				statistics.update(v)
			}

			Expect(statistics.maxRtt).To(Equal(uint64(testMaxRTT)))
			Expect(statistics.minRtt).To(Equal(uint64(testMinRTT)))
			Expect(statistics.lastRtt).To(Equal(uint64(testLastRTT)))
			Expect(statistics.mean).To(Equal(uint64(expectedMean)))
			Expect(statistics.stdDev).To(Equal(uint64(expectedSD)))

			statistics.update(testNewMinRTT)
			statistics.update(testNewMaxRTT)
			statistics.update(testNewLastRTT)

			newExpectedMean := 830998
			newExpectedSD := 272450

			Expect(statistics.maxRtt).To(Equal(uint64(testNewMaxRTT)))
			Expect(statistics.minRtt).To(Equal(uint64(testNewMinRTT)))
			Expect(statistics.lastRtt).To(Equal(uint64(testNewLastRTT)))
			Expect(statistics.mean).To(Equal(uint64(newExpectedMean)))
			Expect(statistics.stdDev).To(Equal(uint64(newExpectedSD)))
		})
	})
})
