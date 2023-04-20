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
	"fmt"
	"sync"
	"time"

	"github.com/pkg/errors"
	probing "github.com/prometheus-community/pro-bing"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

const (
	Privileged         = true
	doPingRetryTimeout = 5
)

var (
	defaultMaxPacketLossCount uint = 5

	defaultPingInterval = 1 * time.Second

	// The RTT will be stored and will be used to calculate the statistics until
	// the size is reached. Once the size is reached the array will be reset and
	// the last elements will be added to the array for statistics.
	size uint64 = 1000

	// Even though we set up the pinger to run continuously, we still have to give it a non-zero timeout else it will
	// fail so set a really long one.
	defaultPingTimeout = 87600 * time.Hour
)

type PingerInterface interface {
	Start()
	Stop()
	GetLatencyInfo() *LatencyInfo
	GetIP() string
}

type PingerConfig struct {
	IP                 string
	Interval           time.Duration
	Timeout            time.Duration
	MaxPacketLossCount uint
}

type pingerInfo struct {
	sync.Mutex
	ip                 string
	pingInterval       time.Duration
	pingTimeout        time.Duration
	maxPacketLossCount uint
	statistics         statistics
	failureMsg         string
	connectionStatus   ConnectionStatus
	stopCh             chan struct{}
}

func NewPinger(config PingerConfig) PingerInterface {
	p := &pingerInfo{
		ip:                 config.IP,
		pingInterval:       config.Interval,
		pingTimeout:        config.Timeout,
		maxPacketLossCount: config.MaxPacketLossCount,
		statistics: statistics{
			size:         size,
			previousRtts: make([]uint64, size),
		},
		stopCh: make(chan struct{}),
	}

	if p.maxPacketLossCount == 0 {
		p.maxPacketLossCount = defaultMaxPacketLossCount
	}

	if p.pingInterval == 0 {
		p.pingInterval = defaultPingInterval
	}

	if p.pingTimeout == 0 {
		p.pingTimeout = defaultPingTimeout
	}

	return p
}

func (p *pingerInfo) Start() {
	logger.Infof("Starting pinger for IP %q", p.ip)

	go func() {
		for {
			select {
			case <-p.stopCh:
				return
			default:
				err := p.doPing()
				if err != nil {
					logger.Errorf(err, "Unable to start pinger for IP %q - retry in %d seconds", p.ip, doPingRetryTimeout)
					time.Sleep(doPingRetryTimeout * time.Second)
				}
			}
		}
	}()
}

func (p *pingerInfo) Stop() {
	select {
	case <-p.stopCh:
		return
	default:
		close(p.stopCh)
	}
}

func (p *pingerInfo) doPing() error {
	pinger, err := probing.NewPinger(p.ip)
	if err != nil {
		p.connectionStatus = ConnectionUnknown
		p.failureMsg = fmt.Sprintf("Failed to create the pinger for the remote endpoint IP %q: %v", p.ip, err)

		return errors.Wrapf(err, "error creating the pinger")
	}

	pinger.Interval = p.pingInterval
	pinger.SetPrivileged(Privileged)
	pinger.RecordRtts = false
	pinger.Timeout = p.pingTimeout

	pinger.OnSend = func(packet *probing.Packet) {
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

			if p.connectionStatus != ConnectionError {
				logger.Errorf(fmt.Errorf("more than %d packets lost", p.maxPacketLossCount),
					"Failed to successfully ping the remote endpoint IP %q", p.ip)
			}

			p.connectionStatus = ConnectionError
			p.failureMsg = fmt.Sprintf("Failed to successfully ping the remote endpoint IP %q", p.ip)

			pinger.Stop()
		}
	}

	pinger.OnRecv = func(packet *probing.Packet) {
		p.Lock()
		defer p.Unlock()

		if p.connectionStatus != Connected {
			logger.Infof("Ping to remote endpoint IP %q is successful", p.ip)
		}

		p.connectionStatus = Connected
		p.failureMsg = ""
		p.statistics.update(uint64(packet.Rtt.Nanoseconds()))

		pinger.PacketsSent = 0
		pinger.PacketsRecv = 0
	}

	err = pinger.Run()
	if err != nil {
		p.connectionStatus = ConnectionUnknown
		p.failureMsg = fmt.Sprintf("Failed to run the pinger for the remote endpoint IP %q: %v", p.ip, err)

		return errors.Wrapf(err, "error running the pinger")
	}

	return nil
}

func (p *pingerInfo) GetIP() string {
	return p.ip
}

func (p *pingerInfo) GetLatencyInfo() *LatencyInfo {
	p.Lock()
	defer p.Unlock()

	return &LatencyInfo{
		ConnectionStatus: p.connectionStatus,
		ConnectionError:  p.failureMsg,
		Spec: &submarinerv1.LatencyRTTSpec{
			Last:    time.Duration(p.statistics.lastRtt).String(),
			Min:     time.Duration(p.statistics.minRtt).String(),
			Average: time.Duration(p.statistics.mean).String(),
			Max:     time.Duration(p.statistics.maxRtt).String(),
			StdDev:  time.Duration(p.statistics.stdDev).String(),
		},
	}
}
