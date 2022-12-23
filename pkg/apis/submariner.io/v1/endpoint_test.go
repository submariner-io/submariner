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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

var _ = Describe("EndpointSpec", func() {
	Context("GenerateName", testGenerateName)
	Context("Equals", testEquals)
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

func testEquals() {
	var spec *v1.EndpointSpec

	BeforeEach(func() {
		spec = &v1.EndpointSpec{
			ClusterID: "east",
			CableName: "submariner-cable-east-172-16-32-5",
			Hostname:  "my-host",
			Backend:   "libreswan",
		}
	})

	Context("with equal scalar fields", func() {
		Context("and nil BackendConfig maps", func() {
			It("should return true", func() {
				Expect(spec.Equals(spec.DeepCopy())).To(BeTrue())
			})
		})

		Context("and equal BackendConfig maps", func() {
			It("should return true", func() {
				spec.BackendConfig = map[string]string{"key": "aaa"}
				Expect(spec.Equals(spec.DeepCopy())).To(BeTrue())
			})
		})

		Context("and empty BackendConfig maps", func() {
			It("should return true", func() {
				spec.BackendConfig = map[string]string{}
				Expect(spec.Equals(spec.DeepCopy())).To(BeTrue())
			})
		})
	})

	Context("with differing ClusterID fields", func() {
		It("should return false", func() {
			other := spec.DeepCopy()
			other.ClusterID = "west"
			Expect(spec.Equals(other)).To(BeFalse())
		})
	})

	Context("with differing CableName fields", func() {
		It("should return false", func() {
			other := spec.DeepCopy()
			other.CableName = "submariner-cable-east-5-6-7-8"
			Expect(spec.Equals(other)).To(BeFalse())
		})
	})

	Context("with differing Hostname fields", func() {
		It("should return false", func() {
			other := spec.DeepCopy()
			other.Hostname = "other-host"
			Expect(spec.Equals(other)).To(BeFalse())
		})
	})

	Context("with differing Backend fields", func() {
		It("should return false", func() {
			other := spec.DeepCopy()
			other.Backend = "wireguard"
			Expect(spec.Equals(other)).To(BeFalse())
		})
	})

	Context("with differing BackendConfig maps", func() {
		It("should return false", func() {
			other := spec.DeepCopy()
			other.BackendConfig = map[string]string{"key": "bbb"}
			spec.BackendConfig = map[string]string{"key": "aaa"}
			Expect(spec.Equals(other)).To(BeFalse())
		})
	})
}
