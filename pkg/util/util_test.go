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

package util_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/util"
)

var _ = Describe("Util", func() {
	Describe("Function GetEndpointCRDName", testGetEndpointCRDName)

	Describe("Function GetClusterCRDName", testGetClusterCRDName)

	Describe("Function CompareEndpointSpec", testCompareEndpointSpec)

	Describe("Function EnsureValidName", testEnsureValidName)
})

func testGetEndpointCRDName() {
	Context("with valid SubmarinerEndpoint input", func() {
		It("should return <cluster ID>-<cable name>", func() {
			name, err := util.GetEndpointCRDName(&types.SubmarinerEndpoint{
				Spec: subv1.EndpointSpec{
					ClusterID: "ClusterID",
					CableName: "CableName",
				},
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(name).To(Equal("clusterid-cablename"))
		})
	})

	Context("with a nil cluster ID", func() {
		It("should return an error", func() {
			_, err := util.GetEndpointCRDName(&types.SubmarinerEndpoint{
				Spec: subv1.EndpointSpec{
					CableName: "CableName",
				},
			})

			Expect(err).To(HaveOccurred())
		})
	})

	Context("with a nil cable name", func() {
		It("should return an error", func() {
			_, err := util.GetEndpointCRDName(&types.SubmarinerEndpoint{
				Spec: subv1.EndpointSpec{
					ClusterID: "ClusterID",
				},
			})

			Expect(err).To(HaveOccurred())
		})
	})
}

func testGetClusterCRDName() {
	Context("with valid input", func() {
		It("should return the cluster ID", func() {
			Expect(util.GetClusterCRDName(&types.SubmarinerCluster{
				Spec: subv1.ClusterSpec{
					ClusterID: "ClusterID",
				},
			})).To(Equal("ClusterID"))
		})
	})

	Context("with a nil cluster ID", func() {
		It("should return an error", func() {
			_, err := util.GetClusterCRDName(&types.SubmarinerCluster{
				Spec: subv1.ClusterSpec{},
			})

			Expect(err).To(HaveOccurred())
		})
	})
}

func testCompareEndpointSpec() {
	Context("with equal input", func() {
		It("should return true", func() {
			Expect(util.CompareEndpointSpec(
				&subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-172-16-32-5",
					Hostname:  "my-host",
					Backend:   "libreswan",
				},
				&subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-172-16-32-5",
					Hostname:  "my-host",
					Backend:   "libreswan",
				})).To(BeTrue())
		})
	})

	Context("with equal input (include backend map)", func() {
		It("should return true", func() {
			Expect(util.CompareEndpointSpec(
				&subv1.EndpointSpec{
					ClusterID:     "east",
					CableName:     "submariner-cable-east-172-16-32-5",
					Hostname:      "my-host",
					Backend:       "libreswan",
					BackendConfig: map[string]string{"key": "aaa"},
				},
				&subv1.EndpointSpec{
					ClusterID:     "east",
					CableName:     "submariner-cable-east-172-16-32-5",
					Hostname:      "my-host",
					Backend:       "libreswan",
					BackendConfig: map[string]string{"key": "aaa"},
				})).To(BeTrue())
		})

		It("should return true", func() {
			Expect(util.CompareEndpointSpec(
				&subv1.EndpointSpec{
					ClusterID:     "east",
					CableName:     "submariner-cable-east-172-16-32-5",
					Hostname:      "my-host",
					Backend:       "libreswan",
					BackendConfig: map[string]string{},
				},
				&subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-172-16-32-5",
					Hostname:  "my-host",
					Backend:   "libreswan",
				})).To(BeTrue())
		})
	})

	Context("with different cluster IDs", func() {
		It("should return false", func() {
			Expect(util.CompareEndpointSpec(
				&subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-172-16-32-5",
					Hostname:  "my-host",
					Backend:   "libreswan",
				},
				&subv1.EndpointSpec{
					ClusterID: "west",
					CableName: "submariner-cable-east-172-16-32-5",
					Hostname:  "my-host",
					Backend:   "libreswan",
				})).To(BeFalse())
		})
	})

	Context("with different cable names", func() {
		It("should return false", func() {
			Expect(util.CompareEndpointSpec(
				&subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-1-2-3-4",
					Hostname:  "my-host",
					Backend:   "libreswan",
				},
				&subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-5-6-7-8",
					Hostname:  "my-host",
					Backend:   "libreswan",
				})).To(BeFalse())
		})
	})

	Context("with different host names", func() {
		It("should return false", func() {
			Expect(util.CompareEndpointSpec(
				&subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-172-16-32-5",
					Hostname:  "host1",
					Backend:   "libreswan",
				},
				&subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-172-16-32-5",
					Hostname:  "host2",
					Backend:   "libreswan",
				})).To(BeFalse())
		})
	})

	Context("with different backend names", func() {
		It("should return false", func() {
			Expect(util.CompareEndpointSpec(
				&subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-172-16-32-5",
					Hostname:  "host1",
					Backend:   "libreswan",
				},
				&subv1.EndpointSpec{
					ClusterID: "east",
					CableName: "submariner-cable-east-172-16-32-5",
					Hostname:  "host1",
					Backend:   "wireguard",
				})).To(BeFalse())
		})
	})

	Context("with different backend parameters", func() {
		It("should return false", func() {
			Expect(util.CompareEndpointSpec(
				&subv1.EndpointSpec{
					ClusterID:     "east",
					CableName:     "submariner-cable-east-172-16-32-5",
					Hostname:      "host1",
					Backend:       "libreswan",
					BackendConfig: map[string]string{"key": "aaa"},
				},
				&subv1.EndpointSpec{
					ClusterID:     "east",
					CableName:     "submariner-cable-east-172-16-32-5",
					Hostname:      "host1",
					Backend:       "libreswan",
					BackendConfig: map[string]string{"key": "bbb"},
				})).To(BeFalse())
		})
	})
}

func testEnsureValidName() {
	When("the string is valid", func() {
		It("should not convert it", func() {
			Expect(util.EnsureValidName("digits-1234567890")).To(Equal("digits-1234567890"))
			Expect(util.EnsureValidName("example.com")).To(Equal("example.com"))
		})
	})

	When("the string has upper case letters", func() {
		It("should convert to lower", func() {
			Expect(util.EnsureValidName("No-UPPER-caSe-aLLoweD")).To(Equal("no-upper-case-allowed"))
		})
	})

	When("the string has non-alphanumeric letters", func() {
		It("should convert them approriately", func() {
			Expect(util.EnsureValidName("no-!@*()#$-chars")).To(Equal("no---------chars"))
		})
	})
}
