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

package v1

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const expectedString = `{"metadata":{"creationTimestamp":null},"spec":{"cluster_id":"cluster-id","cable_name":` +
	`"cable-1","hostname":"","subnets":["10.0.0.0/24","172.0.0.0/24"],"private_ip":"1.1.1.1",` +
	`"public_ip":"","nat_enabled":false,"backend":""}}`

var _ = Describe("API v1", func() {
	When("Endpoint String representation called", func() {
		It("Should return a human readable string", func() {

			endpoint := Endpoint{
				Spec: EndpointSpec{
					ClusterID: "cluster-id",
					Subnets:   []string{"10.0.0.0/24", "172.0.0.0/24"},
					CableName: "cable-1",
					PublicIP:  "",
					PrivateIP: "1.1.1.1",
				},
			}

			Expect(endpoint.String()).To(Equal(expectedString))
		})
	})
})

func TestApiMethods(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "V1 Api Method suite")
}
