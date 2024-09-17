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

package pinger

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Statistics", func() {
	const (
		testMinRTT     int64 = 404351
		testMaxRTT     int64 = 1048263
		testLastRTT    int64 = 1044609
		testNewMinRTT  int64 = 404300
		testNewMaxRTT  int64 = 1048264
		testNewLastRTT int64 = 609555
	)

	When("update is called with a sample space", func() {
		It("should correctly compute the statistics", func() {
			size := 10
			statistics := &statistics{
				size:         int64(size),
				previousRtts: make([]int64, size),
			}

			sampleSpace := [10]int64{testMinRTT, 490406, 530333, 609556, 609650, 685106, 726265, 785707, testMaxRTT, testLastRTT}
			expectedMean := int64(693424)
			expectedSD := int64(205994)

			for _, v := range sampleSpace {
				statistics.update(v)
			}

			Expect(statistics.maxRtt).To(Equal(testMaxRTT))
			Expect(statistics.minRtt).To(Equal(testMinRTT))
			Expect(statistics.lastRtt).To(Equal(testLastRTT))
			Expect(statistics.mean).To(Equal(expectedMean))
			Expect(statistics.stdDev).To(Equal(expectedSD))

			statistics.update(testNewMinRTT)
			statistics.update(testNewMaxRTT)
			statistics.update(testNewLastRTT)

			newExpectedMean := int64(830998)
			newExpectedSD := int64(272450)

			Expect(statistics.maxRtt).To(Equal(testNewMaxRTT))
			Expect(statistics.minRtt).To(Equal(testNewMinRTT))
			Expect(statistics.lastRtt).To(Equal(testNewLastRTT))
			Expect(statistics.mean).To(Equal(newExpectedMean))
			Expect(statistics.stdDev).To(Equal(newExpectedSD))
		})
	})
})
