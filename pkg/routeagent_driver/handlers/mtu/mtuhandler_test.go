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
package mtu

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/iptables"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	fakeNetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/cni"
)

type testDriver struct {
	netLink *fakeNetlink.NetLink
	handler event.Handler
}

var _ = Describe("MTUHandler", func() {
	Describe("Init", testInit)
})

func newTestDriver() *testDriver {
	t := &testDriver{}

	BeforeEach(func() {
		t.netLink = fakeNetlink.New()
		netlinkAPI.NewFunc = func() netlinkAPI.Interface {
			return t.netLink
		}
		t.handler = NewMTUHandler()
		Expect(t.handler.Init()).To(Succeed())
	})

	AfterEach(func() {
		iptables.NewFunc = nil
		cni.DiscoverFunc = nil
	})

	return t
}

func testInit() {
	t := newTestDriver()

	When("MTUHandler is initialized", func() {
		It("Expect the mtuProbe and baseMss to be configured", func() {
			t.netLink.VerifyMtuProbe(mtuProbe)
			t.netLink.VerifyBaseMss(baseMss)
		})
	})
}
