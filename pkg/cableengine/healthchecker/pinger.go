package healthchecker

import (
	"time"

	"github.com/go-ping/ping"
	"k8s.io/klog"
)

var waitTime time.Duration = 15
var size uint64 = 1000

type Pinger struct {
	healthCheckIp string
	statistics    Statistics
	Status        string
	stopCh        chan struct{}
	pinger        *ping.Pinger
}

func NewPinger(healthCheckIP string) *Pinger {
	return &Pinger{
		healthCheckIp: healthCheckIP,
		statistics: Statistics{
			size:         size,
			previousRtts: make([]uint64, size),
		},
		stopCh: make(chan struct{}),
	}
}

func (p *Pinger) Run() {
	go func() {
		var err error

		for {
			select {
			case <-p.stopCh:
				return
			default:
				// Wait for a while for the connection to be ready.
				time.Sleep(waitTime * time.Second)

				p.pinger, err = ping.NewPinger(p.healthCheckIp)
				if err != nil {
					klog.Errorf("Creating pinger for the ip %v failed due to %v", p.healthCheckIp, err)

					return
				}

				p.sendPing(p.pinger, p.healthCheckIp)
			}
		}
	}()
	klog.Infof("CableEngine HealthChecker started")
}

func (p *Pinger) stop() {
	p.pinger.Stop()
}

func (p *Pinger) sendPing(pinger *ping.Pinger, healthCheckIp string) {
	pinger.SetPrivileged(true)
	pinger.RecordRtts = false

	pinger.OnRecv = func(packet *ping.Packet) {
		p.Status = ""
		p.statistics.updateStatistics(uint64(packet.Rtt.Nanoseconds()))
	}

	pinger.OnFinish = func(stats *ping.Statistics) {
		p.Status = "Failed to successfully ping the remote endpoint"
	}
	err := pinger.Run()
	if err != nil {
		klog.Errorf("Ping to ip %q failed due to  %v", healthCheckIp, err)
	}
}
