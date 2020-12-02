package healthchecker

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/go-ping/ping"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"k8s.io/klog"
)

var defaultMaxPacketLossCount uint = 5

// The RTT will be stored and will be used to calculate the statistics until
// the size is reached. Once the size is reached the array will be reset and
// the last elements will be added to the array for statistics.
var size uint64 = 1000

var defaultPingInterval = 1 * time.Second

type PingerInterface interface {
	Start()
	Stop()
	GetLatencyInfo() *LatencyInfo
	GetIP() string
}

type pingerInfo struct {
	sync.Mutex
	ip                 string
	pingInterval       time.Duration
	maxPacketLossCount uint
	statistics         statistics
	failureMsg         string
	stopCh             chan struct{}
}

func newPinger(ip string, pingInterval time.Duration, maxPacketLossCount uint) PingerInterface {
	return &pingerInfo{
		ip:                 ip,
		pingInterval:       pingInterval,
		maxPacketLossCount: maxPacketLossCount,
		statistics: statistics{
			size:         size,
			previousRtts: make([]uint64, size),
		},
		stopCh: make(chan struct{}),
	}
}

func (p *pingerInfo) Start() {
	go func() {
		for {
			select {
			case <-p.stopCh:
				return
			default:
				err := p.doPing()
				if err != nil {
					klog.Errorf("Unable to start pinger for IP %q: %v", p.ip, err)
					return
				}
			}
		}
	}()
}

func (p *pingerInfo) Stop() {
	close(p.stopCh)
}

func (p *pingerInfo) doPing() error {
	klog.Infof("Starting pinger for IP %q", p.ip)

	pinger, err := ping.NewPinger(p.ip)
	if err != nil {
		return err
	}

	pinger.Interval = p.pingInterval
	pinger.SetPrivileged(true)
	pinger.RecordRtts = false

	pinger.OnSend = func(packet *ping.Packet) {
		select {
		case <-p.stopCh:
			pinger.Stop()
			return
		default:
		}

		// Pinger will mark a connection as an error if the packet loss reaches the threshold
		if pinger.PacketsSent-pinger.PacketsRecv > int(p.maxPacketLossCount) {
			p.Lock()
			defer p.Unlock()

			p.failureMsg = fmt.Sprintf("Failed to successfully ping the remote endpoint IP %q", p.ip)
			pinger.PacketsSent = 0
			pinger.PacketsRecv = 0
		}
	}

	pinger.OnRecv = func(packet *ping.Packet) {
		p.Lock()
		defer p.Unlock()

		p.failureMsg = ""
		p.statistics.update(uint64(packet.Rtt.Nanoseconds()))
	}

	err = pinger.Run()
	if err != nil {
		return err
	}

	klog.Infof("Pinger for IP %q stopped", p.ip)

	return nil
}

func (p *pingerInfo) GetIP() string {
	return p.ip
}

func (p *pingerInfo) GetLatencyInfo() *LatencyInfo {
	p.Lock()
	defer p.Unlock()

	lastTime, _ := time.ParseDuration(strconv.FormatUint(p.statistics.lastRtt, 10) + "ns")
	minTime, _ := time.ParseDuration(strconv.FormatUint(p.statistics.minRtt, 10) + "ns")
	averageTime, _ := time.ParseDuration(strconv.FormatUint(p.statistics.mean, 10) + "ns")
	maxTime, _ := time.ParseDuration(strconv.FormatUint(p.statistics.maxRtt, 10) + "ns")
	stdDevTime, _ := time.ParseDuration(strconv.FormatUint(p.statistics.stdDev, 10) + "ns")

	return &LatencyInfo{
		ConnectionError: p.failureMsg,
		Spec: &submarinerv1.LatencyRTTSpec{
			Last:    lastTime.String(),
			Min:     minTime.String(),
			Average: averageTime.String(),
			Max:     maxTime.String(),
			StdDev:  stdDevTime.String(),
		},
	}
}
