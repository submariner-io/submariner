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

package healthchecker

import (
	"net"
	"os"
	"syscall"
	"time"

	"github.com/go-ping/ping"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

/**
  These tests send/receive real ICMP messages which requires root or certain privileges as described
  at https://github.com/go-ping/ping. If running locally outside of the dapper image you may need to make tweaks
  accordingly.
*/
var _ = Describe("Pinger", func() {
	var (
		pinger             PingerInterface
		ip                 string
		pingInterval       time.Duration
		maxPacketLossCount uint
		testsEnabled       bool
	)

	testsEnabled = func() bool {
		// Run a pinger to check if listening on an ICMP socket is permitted.
		err := func() error {
			p, err := ping.NewPinger("127.0.0.1")
			if err != nil {
				return err
			}

			p.Count = 1
			p.Timeout = 50 * time.Millisecond
			p.SetPrivileged(privileged)

			return p.Run()
		}()

		if opErr, ok := err.(*net.OpError); ok {
			if sysCallErr, ok := opErr.Unwrap().(*os.SyscallError); ok {
				if errNo, ok := sysCallErr.Unwrap().(syscall.Errno); ok {
					// errNo 1 is "operation not permitted".
					return !(opErr.Op == "listen" && sysCallErr.Syscall == "socket" && errNo == 1)
				}
			}
		}

		return true
	}()

	BeforeEach(func() {
		if !testsEnabled {
			Skip("Ping operation not permitted, skipping the test...")
			return
		}

		ip = "127.0.0.1"
		pingInterval = 300 * time.Millisecond
		maxPacketLossCount = defaultMaxPacketLossCount
	})

	JustBeforeEach(func() {
		pinger = newPinger(ip, pingInterval, maxPacketLossCount)
		pinger.Start()
	})

	AfterEach(func() {
		pinger.Stop()
	})

	verifyPingStats := func(count int) {
		last := &LatencyInfo{}

		for i := 0; i < count; i++ {
			var current *LatencyInfo

			Eventually(func() *LatencyInfo {
				current = pinger.GetLatencyInfo()
				return current
			}, pingInterval*2).ShouldNot(Equal(last))

			last = current
		}
	}

	When("the IP is reachable", func() {
		It("should periodically update the statistics", func() {
			verifyPingStats(5)
		})
	})

	When("the IP is not reachable", func() {
		BeforeEach(func() {
			ip = "3.4.5.6"
			pingInterval = 100 * time.Millisecond
		})

		It("should mark a failure", func() {
			Eventually(func() string {
				return pinger.GetLatencyInfo().ConnectionError
			}, 5*time.Second).Should(Not(BeEmpty()))
		})
	})

	When("the pinger timeout expires", func() {
		BeforeEach(func() {
			pingTimeout = 300
			pingInterval = 2 * time.Second
		})

		AfterEach(func() {
			pingTimeout = defaultPingTimeout
		})

		It("should continue to update the statistics", func() {
			verifyPingStats(3)
		})
	})

	When("the pinger is stopped", func() {
		BeforeEach(func() {
			pingInterval = 100 * time.Millisecond
		})

		It("should no longer update the statistics", func() {
			verifyPingStats(3)

			pinger.Stop()

			time.Sleep(pingInterval * 2)

			current := pinger.GetLatencyInfo()
			Consistently(pinger.GetLatencyInfo, pingInterval*10).Should(Equal(current))
		})
	})
})
