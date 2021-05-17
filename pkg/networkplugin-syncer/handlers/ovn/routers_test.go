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
package ovn

import (
	goovn "github.com/ebay/go-ovn"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var portA = "port-a"
var portB = "port-b"

var _ = Describe("Route functions", func() {
	It("should filter routes via specific output port correctly", func() {
		lrs := []*goovn.LogicalRouterStaticRoute{
			{OutputPort: &portA, IPPrefix: cluster1Net1},
			{OutputPort: &portA, IPPrefix: cluster1Net2},
			{OutputPort: &portB, IPPrefix: cluster2Net1},
			{OutputPort: &portB, IPPrefix: cluster2Net2},
		}
		toPortA := filterRouteSubnetsViaPort(lrs, portA)
		Expect(toPortA.Elements()).To(HaveLen(2))
		Expect(toPortA.Elements()).To(ContainElements(cluster1Net1, cluster1Net2))

		toPortB := filterRouteSubnetsViaPort(lrs, portB)
		Expect(toPortB.Elements()).To(HaveLen(2))
		Expect(toPortB.Elements()).To(ContainElements(cluster2Net1, cluster2Net2))
	})
})
