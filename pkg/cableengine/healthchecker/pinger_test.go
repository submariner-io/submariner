package healthchecker

import (
	"time"

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
	)

	BeforeEach(func() {
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
