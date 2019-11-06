package ipam

import (
	"encoding/binary"
	"math"
	"net"
	"sync"
)

type IpPool struct {
	cidr string
	net  *net.IPNet
	size int
	available map[string]bool //IP.String() is key
	allocated map[string]string //resource name is key, ipString is value
	sync.RWMutex
}

func NewIpPool(cidr string) (*IpPool, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}
	ones, totalbits := network.Mask.Size()
	size := int(math.Exp2(float64(totalbits - ones))) - 2 // don't count net and broadcast
	pool := &IpPool{
		cidr: cidr,
		net: network,
		size: size,
		available: make(map[string]bool),
		allocated: make(map[string]string),
	}
	startingIp := ipToInt(pool.net.IP) + 1
	for i := 0; i < pool.size; i++ {
		ip := intToIP(startingIp + i).String()
		pool.available[ip] = true
	}
	return pool, nil
}

func ipToInt(ip net.IP) int {
	intIp := ip
	if len(ip) == 16 {
		intIp = ip[12:16]
	}
	return int(binary.BigEndian.Uint32(intIp))
}

func intToIP(ip int) net.IP {
	netIp := make(net.IP, 4)
	binary.BigEndian.PutUint32(netIp, uint32(ip))
	return netIp
}

func (p *IpPool) Allocate(key string) string{
	p.Lock()
	allocatedIp := p.allocated[key]
	if  allocatedIp == "" {
		for k := range p.available {
			p.allocated[key] = k
			delete(p.available, k)
			p.Unlock()
			return k
		}
		p.Unlock()
		return ""
	}
	p.Unlock()
	return allocatedIp
}

func (p *IpPool) Release(key string) string {
	p.Lock()
	ip := p.allocated[key]
	if ip != "" {
		p.available[ip] = true
		delete(p.allocated, key)
	}
	p.Unlock()
	return ip
}

func (p *IpPool) IsAvailable (ip string) bool {
	return p.available[ip]
}

func (p *IpPool) GetAllocatedIp (name string) string {
	p.RLock()
	ip := p.allocated[name]
	p.RUnlock()
	return ip
}

func (p *IpPool) IsFull () bool {
	var result bool
	p.RLock()
	result = len(p.available) == 0
	p.RUnlock()
	return result
}

func (p *IpPool) RequestIp(key string, ip string) string {
	if p.GetAllocatedIp(key) == ip {
		return ip
	}
	if p.IsAvailable(ip) {
		p.Lock()
		p.allocated[key] = ip
		delete(p.available, ip)
		p.Unlock()
		return ip
	}
	// It is neither allocated for this key, nor available, give another.
	return p.Allocate(key)
}
