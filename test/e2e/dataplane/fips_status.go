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

package dataplane

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	subFramework "github.com/submariner-io/submariner/test/e2e/framework"
)

var _ = Describe("[dataplane] FIPS", func() {
	f := subFramework.NewFramework("fips-gateway-status")

	When("FIPS mode is enabled for the active gateway node", func() {
		It("should use FIPS mode in the libreswan cable driver", func() {
			testFIPSGatewayStatus(f)
		})
	})
})

func testFIPSGatewayStatus(f *subFramework.Framework) {
	By(fmt.Sprintln("Find a cluster with FIPS enabled"))

	fipsCluster := f.FindFIPSEnabledCluster()

	if fipsCluster == -1 {
		framework.Skipf("No cluster found with FIPS enabled, skipping the test...")
	}

	fipsClusterName := framework.TestContext.ClusterIDs[fipsCluster]
	By(fmt.Sprintf("Found enabled FIPS on cluster %q", fipsClusterName))

	submEndpoint := f.AwaitSubmarinerEndpoint(fipsCluster, subFramework.NoopCheckEndpoint)

	if submEndpoint.Spec.Backend != "libreswan" {
		framework.Skipf(fmt.Sprintf("Cluster %q is not using the libreswan cable driver, skipping the test...", fipsClusterName))
	}

	By(fmt.Sprintf("Locate active gateway pod on cluster %q", fipsClusterName))

	gwPod := f.AwaitActiveGatewayPod(fipsCluster, nil)
	Expect(gwPod).ToNot(BeNil(), "Did not find an active gateway pod")

	f.TestGatewayNodeFIPSMode(fipsCluster, gwPod.Name)
}
