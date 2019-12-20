package ipam

import (
	"encoding/binary"
	"errors"
	"math"
	"net"
	"sync"
)

type IpPool struct {
	cidr      string
	net       *net.IPNet
	size      int
	available map[string]bool   //IP.String() is key
	allocated map[string]string //resource "namespace/name" is key, ipString is value
	sync.RWMutex
}

func NewIpPool(cidr string) (*IpPool, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}
	ones, totalbits := network.Mask.Size()
	size := int(math.Exp2(float64(totalbits-ones))) - 2 // don't count net and broadcast
	if size < 2 {
		return nil, errors.New("Invalid CIDR Prefix")
	}
	pool := &IpPool{
		cidr:      cidr,
		net:       network,
		size:      size,
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

func (p *IpPool) Allocate(key string) (string, error) {
	if p.IsFull() {
		return "", errors.New("IPAM: No IP available for allocation")
	}
	p.Lock()
	defer p.Unlock()
	allocatedIp := p.allocated[key]
	if allocatedIp == "" {
		for k := range p.available {
			p.allocated[key] = k
			delete(p.available, k)
			return k, nil
		}
		return "", errors.New("IPAM: Unable to allocate IP")
	}
	return allocatedIp, nil
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

func (p *IpPool) IsAvailable(ip string) bool {
	p.RLock()
	result := p.available[ip]
	p.RUnlock()
	return result
}

func (p *IpPool) GetAllocatedIp(key string) string {
	p.RLock()
	ip := p.allocated[key]
	p.RUnlock()
	return ip
}

func (p *IpPool) IsFull() bool {
	var result bool
	p.RLock()
	result = len(p.available) == 0
	p.RUnlock()
	return result
}

func (p *IpPool) RequestIp(key string, ip string) (string, error) {
	if p.GetAllocatedIp(key) == ip {
		return ip, nil
	}
	if p.IsAvailable(ip) {
		p.Lock()
		p.allocated[key] = ip
		delete(p.available, ip)
		p.Unlock()
		return ip, nil
	}
	// It is neither allocated for this key, nor available, give another.
	return p.Allocate(key)
}
