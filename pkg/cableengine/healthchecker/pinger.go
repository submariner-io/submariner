package healthchecker

import (
	"math"
	"time"

	"github.com/go-ping/ping"
	"k8s.io/klog"
)

var waitTime time.Duration = 15
var size uint64 = 1000

type Pinger struct {
	remoteEndpointName string
	healthCheckIp      string
	statistics         Statistics
	Status             string
	stopCh             chan struct{}
	pinger             *ping.Pinger
}

type Statistics struct {
	previousRtts []uint64
	sum          uint64
	mean         uint64
	stdDev       uint64
	lastRtt      uint64
	minRtt       uint64
	maxRtt       uint64
	sqrDiff      uint64
	index        uint64
	size         uint64
}

func NewPinger(localEndpointName, healthCheckIP string) *Pinger {
	return &Pinger{
		remoteEndpointName: localEndpointName,
		healthCheckIp:      healthCheckIP,
		statistics: Statistics{
			size:         size,
			previousRtts: make([]uint64, size),
		},
		stopCh: make(chan struct{}),
	}
}

func (s *Statistics) updateStatistics(rtt uint64) {
	s.lastRtt = rtt

	if (s.index + 1) > 1 {
		s.previousRtts[s.index%s.size] = rtt
		if s.minRtt == 0 || s.minRtt > rtt {
			s.minRtt = rtt
		}

		if s.maxRtt < rtt {
			s.maxRtt = rtt
		}

		s.sum += rtt
		oldMean := s.mean
		s.mean = s.sum / (s.index + 1)
		s.sqrDiff += uint64(((int64(rtt - oldMean)) * int64((rtt - s.mean))))
		s.stdDev = uint64(math.Sqrt(float64(s.sqrDiff / (s.index + 1))))
	} else {
		s.sum = rtt
		s.sqrDiff = 0
		s.minRtt = rtt
		s.maxRtt = rtt
		s.mean = rtt
		s.sum = rtt
		s.previousRtts[s.index%s.size] = rtt
		s.stdDev = 0
	}

	if (s.index + 1) == s.size {
		// Resetting since the incremental SD calculated have an error factor due to truncation which
		// could be significant as count increases.
		s.index = 1
		s.previousRtts[0] = s.previousRtts[s.size-2]
		s.previousRtts[1] = s.previousRtts[s.size-1]
		s.sum = s.previousRtts[0] + s.previousRtts[1]
		s.mean = s.sum / 2
		s.sqrDiff = uint64((int64(s.previousRtts[0]-s.mean))*(int64(s.previousRtts[0]-s.mean)) +
			(int64(s.previousRtts[1]-s.mean))*(int64(s.previousRtts[1]-s.mean)))
	}

	s.index = (s.index + 1) % s.size
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
					klog.Errorf("Creating pinger for hostname %v to the ip %v failed due to %v", p.remoteEndpointName, p.healthCheckIp, err)

					return
				}

				p.sendPing(p.pinger, p.remoteEndpointName, p.healthCheckIp)
			}
		}
	}()
	klog.Infof("CableEngine HealthChecker started")
}

func (p *Pinger) stop() {
	p.pinger.Stop()
}

func (p *Pinger) sendPing(pinger *ping.Pinger, remoteHostName, healthCheckIp string) {
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
		klog.Errorf("Ping to %q with ip %q failed due to  %v", remoteHostName, healthCheckIp, err)
	}
}
