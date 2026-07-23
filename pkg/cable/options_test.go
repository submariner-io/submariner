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

package cable_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/pkg/cable"
)

var _ = Describe("GetDriverOptions", func() {
	AfterEach(func() {
		Expect(os.Unsetenv(cable.CableDriverOptionsEnv)).To(Succeed())
	})

	When("the environment variable is unset or empty", func() {
		It("should return an empty map", func() {
			Expect(os.Setenv(cable.CableDriverOptionsEnv, "")).To(Succeed())

			options, err := cable.GetDriverOptions()
			Expect(err).NotTo(HaveOccurred())
			Expect(options).To(BeEmpty())
		})
	})

	When("the environment variable contains valid JSON", func() {
		It("should return the parsed options", func() {
			Expect(os.Setenv(cable.CableDriverOptionsEnv, `{"jc":"9","h1":"1-2"}`)).To(Succeed())

			options, err := cable.GetDriverOptions()
			Expect(err).NotTo(HaveOccurred())
			Expect(options).To(Equal(map[string]string{
				"jc": "9",
				"h1": "1-2",
			}))
		})
	})

	When("the environment variable contains invalid JSON", func() {
		It("should return an error", func() {
			Expect(os.Setenv(cable.CableDriverOptionsEnv, `{not-json`)).To(Succeed())

			options, err := cable.GetDriverOptions()
			Expect(err).To(HaveOccurred())
			Expect(options).To(BeNil())
		})
	})
})
