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

package netlink

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/vishvananda/netlink"
)

var _ = Describe("retryOnInterrupted", func() {
	It("should succeed on first attempt", func() {
		result, err := retryOnInterrupted(func() (string, error) {
			return "success", nil
		})
		Expect(err).To(Succeed())
		Expect(result).To(Equal("success"))
	})

	It("should retry on interrupted error and eventually succeed", func() {
		callCount := 0
		result, err := retryOnInterrupted(func() (string, error) {
			callCount++
			if callCount < 4 {
				return "", netlink.ErrDumpInterrupted
			}

			return "success after retries", nil
		})
		Expect(err).To(Succeed())
		Expect(result).To(Equal("success after retries"))
		Expect(callCount).To(Equal(4))
	})

	It("should give up after MaxRetries attempts", func() {
		callCount := 0
		result, err := retryOnInterrupted(func() (string, error) {
			callCount++
			return "", netlink.ErrDumpInterrupted
		})
		Expect(err).To(Equal(netlink.ErrDumpInterrupted))
		Expect(result).To(Equal(""))
		Expect(callCount).To(Equal(MaxRetries))
	})

	It("should not retry on non-interrupt errors", func() {
		otherError := errors.New("some other error")
		callCount := 0
		result, err := retryOnInterrupted(func() (string, error) {
			callCount++
			return "", otherError
		})
		Expect(err).To(Equal(otherError))
		Expect(result).To(Equal(""))
		Expect(callCount).To(Equal(1))
	})
})
