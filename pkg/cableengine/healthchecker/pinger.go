package healthchecker

import (
	"fmt"
	"time"

	"github.com/go-ping/ping"
	"k8s.io/klog"
)

var waitTime time.Duration = 15 * time.Second
var timeout = 3 * time.Second

// The RTT will be stored and will be used to calculate the statistics until
// the size is reached. Once the size is reached the array will be reset and
// the last elements will be added to the array for statistics.
var size uint64 = 1000

type pingerInfo struct {
	healthCheckIP string
	statistics    statistics
	failureMsg    string
	stopCh        chan struct{}
}

func newPinger(healthCheckIP string) *pingerInfo {
	return &pingerInfo{
		healthCheckIP: healthCheckIP,
		statistics: statistics{
			size:         size,
			previousRtts: make([]uint64, size),
		},
		stopCh: make(chan struct{}),
	}
}

func (p *pingerInfo) start() {
	go func() {
		for {
			select {
			case <-p.stopCh:
				return
			case <-time.After(waitTime):
				p.sendPing()
			}
		}
	}()
	klog.Infof("CableEngine HealthChecker started pinger for IP %q", p.healthCheckIP)
}

func (p *pingerInfo) stop() {
	close(p.stopCh)
}

func (p *pingerInfo) sendPing() {
	pinger, err := ping.NewPinger(p.healthCheckIP)
	if err != nil {
		klog.Errorf("Error creating pinger for IP %q: %v", p.healthCheckIP, err)
		return
	}

	pinger.SetPrivileged(true)
	pinger.RecordRtts = false
	// After 3 seconds stop waiting.
	pinger.Timeout = timeout

	pinger.OnRecv = func(packet *ping.Packet) {
		p.failureMsg = ""
		p.statistics.update(uint64(packet.Rtt.Nanoseconds()))
	}

	pinger.OnFinish = func(stats *ping.Statistics) {
		// Since we are setting a timeout and not a count, it will be an endless ping.
		// If the timeout is reached with no successful packets, onFinish will be called and it is a failed ping.
		p.failureMsg = fmt.Sprintf("Failed to successfully ping the remote endpoint IP %q", p.healthCheckIP)
	}
	err = pinger.Run()
	if err != nil {
		klog.Errorf("Error running ping for the remote endpoint IP %q: %v", p.healthCheckIP, err)
	}
}
