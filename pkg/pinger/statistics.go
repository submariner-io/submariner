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
	"math"
)

type statistics struct {
	previousRtts []int64
	sum          int64
	mean         int64
	stdDev       int64
	lastRtt      int64
	minRtt       int64
	maxRtt       int64
	sqrDiff      int64
	index        int64
	size         int64
}

func (s *statistics) update(rtt int64) {
	s.lastRtt = rtt

	if s.index == s.size {
		// Resetting since the incremental SD calculated have an error factor due to truncation which
		// could be significant as count increases.
		s.index = 2
		s.previousRtts[0] = s.previousRtts[s.size-2]
		s.previousRtts[1] = s.previousRtts[s.size-1]
		s.sum = s.previousRtts[0] + s.previousRtts[1]
		s.mean = s.sum / 2

		s.sqrDiff = (s.previousRtts[0]-s.mean)*(s.previousRtts[0]-s.mean) +
			(s.previousRtts[1]-s.mean)*(s.previousRtts[1]-s.mean)
	}

	if s.index+1 > 1 {
		s.previousRtts[s.index] = rtt
		if s.minRtt == 0 || s.minRtt > rtt {
			s.minRtt = rtt
		}

		if s.maxRtt < rtt {
			s.maxRtt = rtt
		}

		s.sum += rtt
		oldMean := s.mean
		s.mean = s.sum / (s.index + 1)

		s.sqrDiff += (rtt - oldMean) * (rtt - s.mean)
		s.stdDev = int64(math.Sqrt(float64(s.sqrDiff / (s.index + 1))))
	} else {
		s.sum = rtt
		s.sqrDiff = 0
		s.minRtt = rtt
		s.maxRtt = rtt
		s.mean = rtt
		s.sum = rtt
		s.previousRtts[s.index] = rtt
		s.stdDev = 0
	}

	s.index++
}
