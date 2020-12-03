package fake

import (
	"sync/atomic"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/submariner/pkg/cableengine/healthchecker"
)

type Pinger struct {
	ip          string
	latencyInfo atomic.Value
	start       chan struct{}
	stop        chan struct{}
}

func NewPinger(ip string) *Pinger {
	return &Pinger{
		ip:    ip,
		start: make(chan struct{}),
		stop:  make(chan struct{}),
	}
}

func (p *Pinger) Start() {
	defer GinkgoRecover()
	Expect(p.start).ToNot(BeClosed())
	close(p.start)
}

func (p *Pinger) Stop() {
	defer GinkgoRecover()
	Expect(p.stop).ToNot(BeClosed())
	close(p.stop)
}

func (p *Pinger) GetLatencyInfo() *healthchecker.LatencyInfo {
	o := p.latencyInfo.Load()
	if o != nil {
		info := o.(healthchecker.LatencyInfo)
		return &info
	}

	return nil
}

func (p *Pinger) SetLatencyInfo(info *healthchecker.LatencyInfo) {
	p.latencyInfo.Store(*info)
}

func (p *Pinger) GetIP() string {
	return p.ip
}

func (p *Pinger) AwaitStart() {
	Eventually(p.start).Should(BeClosed(), "Start was not called")
}

func (p *Pinger) AwaitStop() {
	Eventually(p.stop).Should(BeClosed(), "Stop was not called")
}
