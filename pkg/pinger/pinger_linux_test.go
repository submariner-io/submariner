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

package pinger_test

import (
	"errors"
	"net"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/pkg/pinger"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

var _ = Describe("Pinger", func() {
	When("sends to the IP start failing", func() {
		It("should mark a failure", func() {
			if !pingPermitted() {
				Skip("Ping operation not permitted, skipping the test...")
			}

			ip := "127.0.0.2"

			// From here every send fails, like behind an IPsec block policy left on a passive gateway. Loopback skips
			// IPsec policies, so an unreachable route stands in for one.
			route := &netlink.Route{
				Dst:   &net.IPNet{IP: net.ParseIP(ip), Mask: net.CIDRMask(32, 32)},
				Type:  unix.RTN_UNREACHABLE,
				Table: unix.RT_TABLE_LOCAL,
			}

			existing, err := netlink.RouteListFiltered(netlink.FAMILY_V4, route, netlink.RT_FILTER_DST|netlink.RT_FILTER_TABLE)
			Expect(err).To(Succeed())

			if len(existing) > 0 {
				Skip("A route to " + ip + " already exists, skipping the test...")
			}

			p := pinger.NewPinger(pinger.Config{IP: ip, Interval: 100 * time.Millisecond})
			p.Start()
			DeferCleanup(p.Stop)

			status := func() pinger.ConnectionStatus {
				return p.GetLatencyInfo().ConnectionStatus
			}

			Eventually(status, 3*time.Second).Should(Equal(pinger.Connected))

			err = netlink.RouteAdd(route)
			if errors.Is(err, syscall.EPERM) {
				Skip("Adding a route not permitted, skipping the test...")
			}

			Expect(err).To(Succeed())
			DeferCleanup(func() {
				_ = netlink.RouteDel(route)
			})

			Eventually(status, 5*time.Second).Should(Equal(pinger.ConnectionError))
		})
	})
})
