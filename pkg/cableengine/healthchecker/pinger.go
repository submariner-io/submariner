package healthchecker

import (
	"fmt"
	"time"

	"github.com/go-ping/ping"
	"k8s.io/klog"
)

var waitTime = 15 * time.Second

const threshold = 5

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

	pinger.OnSend = func(packet *ping.Packet) {
		// Pinger will mark a connection as an error if the packet loss reaches the threshold
		if pinger.PacketsSent-pinger.PacketsRecv > threshold {
			p.failureMsg = fmt.Sprintf("Failed to successfully ping the remote endpoint IP %q", p.healthCheckIP)
			pinger.PacketsSent = 0
			pinger.PacketsRecv = 0
		}
	}

	pinger.OnRecv = func(packet *ping.Packet) {
		p.failureMsg = ""
		p.statistics.update(uint64(packet.Rtt.Nanoseconds()))
	}

	err = pinger.Run()
	if err != nil {
		klog.Errorf("Error running ping for the remote endpoint IP %q: %v", p.healthCheckIP, err)
	}
}
