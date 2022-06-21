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

package v1_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

var _ = Describe("EndpointSpec", func() {
	Context("GenerateName", testGenerateName)
})

func testGenerateName() {
	When("the fields are valid", func() {
		It("should return <cluster ID>-<cable name>", func() {
			name, err := (&v1.EndpointSpec{
				ClusterID: "ClusterID",
				CableName: "CableName",
			}).GenerateName()

			Expect(err).ToNot(HaveOccurred())
			Expect(name).To(Equal("clusterid-cablename"))
		})
	})

	When("the ClusterID is empty", func() {
		It("should return an error", func() {
			_, err := (&v1.EndpointSpec{
				CableName: "CableName",
			}).GenerateName()

			Expect(err).To(HaveOccurred())
		})
	})

	When("the CableName is empty", func() {
		It("should return an error", func() {
			_, err := (&v1.EndpointSpec{
				ClusterID: "ClusterID",
			}).GenerateName()

			Expect(err).To(HaveOccurred())
		})
	})
}
