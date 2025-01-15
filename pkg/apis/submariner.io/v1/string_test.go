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
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

var _ = Describe("Endpoint String", func() {
	It("should return a human readable string", func() {
		str := (&v1.Endpoint{
			Spec: v1.EndpointSpec{
				ClusterID:  "east",
				Subnets:    []string{"10.0.0.0/24"},
				CableName:  "cable-1",
				PublicIPs:  []string{"1.1.1.1"},
				PrivateIPs: []string{"2.2.2.2"},
			},
		}).String()

		Expect(str).To(ContainSubstring("east"))
		Expect(str).To(ContainSubstring("10.0.0.0/24"))
		Expect(str).To(ContainSubstring("cable-1"))
		Expect(str).To(ContainSubstring("1.1.1.1"))
		Expect(str).To(ContainSubstring("2.2.2.2"))
	})
})

func TestApiMethods(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "V1 Api Method suite")
}
