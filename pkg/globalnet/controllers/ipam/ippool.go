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
package ipam

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"sync"

	"github.com/psampaz/gods/maps/treemap"
)

type IPPool struct {
	cidr      string
	net       *net.IPNet
	size      int
	available *treemap.Map      // IntIP is the key, StringIP is value
	allocated map[string]string // resource "namespace/name" is key, ipString is value
	sync.RWMutex
}

func NewIPPool(cidr string) (*IPPool, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}

	ones, totalbits := network.Mask.Size()
	size := int(math.Exp2(float64(totalbits-ones))) - 2 // don't count net and broadcast
	if size < 2 {
		return nil, errors.New("invalid CIDR Prefix")
	}

	pool := &IPPool{
		cidr:      cidr,
		net:       network,
		size:      size,
		available: treemap.NewWithIntComparator(),
		allocated: make(map[string]string),
	}
	startingIP := ipToInt(pool.net.IP) + 1

	for i := 0; i < pool.size; i++ {
		intIP := startingIP + i
		ip := intToIP(startingIP + i).String()
		pool.available.Put(intIP, ip)
	}

	RecordAvailability(cidr, size)

	return pool, nil
}

func ipToInt(ip net.IP) int {
	intIP := ip
	if len(ip) == 16 {
		intIP = ip[12:16]
	}

	return int(binary.BigEndian.Uint32(intIP))
}

func intToIP(ip int) net.IP {
	netIP := make(net.IP, 4)
	binary.BigEndian.PutUint32(netIP, uint32(ip))

	return netIP
}

func StringIPToInt(stringIP string) int {
	return ipToInt(net.ParseIP(stringIP))
}

func (p *IPPool) Allocate(key string) (string, error) {
	p.Lock()
	defer p.Unlock()
	allocatedIP := p.allocated[key]
	if allocatedIP == "" {
		iter := p.available.Iterator()
		if !p.available.Empty() {
			iter.Last()
			ip := iter.Value().(string)
			p.allocated[key] = ip
			p.available.Remove(iter.Key())
			RecordAllocateGlobalIP(p.cidr)

			return ip, nil
		}

		return "", errors.New("IPAM: No IP available for allocation")
	}

	return allocatedIP, nil
}

func (p *IPPool) Release(key string) string {
	p.Lock()
	ip := p.allocated[key]
	if ip != "" {
		p.available.Put(StringIPToInt(ip), ip)
		delete(p.allocated, key)
		RecordDeallocateGlobalIP(p.cidr)
	}
	p.Unlock()

	return ip
}

func (p *IPPool) IsAvailable(ip string) bool {
	p.RLock()
	_, result := p.available.Get(StringIPToInt(ip))
	p.RUnlock()

	return result
}

func (p *IPPool) GetAllocatedIP(key string) string {
	p.RLock()
	ip := p.allocated[key]
	p.RUnlock()

	return ip
}

func (p *IPPool) RequestIPIfAvailable(key, ip string) (string, error) {
	if p.GetAllocatedIP(key) == ip {
		return ip, nil
	}

	intIP := StringIPToInt(ip)

	p.Lock()
	defer p.Unlock()
	_, found := p.available.Get(intIP)
	if found {
		p.allocated[key] = ip
		p.available.Remove(intIP)
		RecordAllocateGlobalIP(p.cidr)

		return ip, nil
	}

	// It is neither allocated for this key, nor available, return error.
	return "", errors.New("IPAM: Requested IP already allocated")
}

func (p *IPPool) RequestIP(key, ip string) (string, error) {
	ip, err := p.RequestIPIfAvailable(key, ip)

	if err != nil {
		// It is neither allocated for this key, nor available, give another.
		return p.Allocate(key)
	}

	return ip, err
}

func (p *IPPool) AllocateIPBlock(key string, num int) ([]string, error) {
	if p.available.Size() < num {
		return nil, errors.New("IPAM: Insufficient IPs available for allocation")
	}

	intIPs := make([]int, num)
	prev, current := 0, 0

	p.Lock()
	defer p.Unlock()

	iter := p.available.Iterator()
	for iter.Next() {
		if current == num {
			return p.allocateBlock(key, intIPs), nil
		}

		intIP := iter.Key().(int)
		intIPs[current] = intIP
		if current == 0 {
			current++
			continue
		}

		prevIntIP := intIPs[prev]
		if prevIntIP+1 != intIP {
			intIPs = make([]int, num)
			prev, current = 0, 0
		} else {
			prev = current
			current++
		}
	}

	return nil, errors.New("IPAM: unable to find contiguous block for allocation")
}

func (p *IPPool) allocateBlock(key string, intIPs []int) []string {
	num := len(intIPs)
	ips := make([]string, num)

	for i := 0; i < num; i++ {
		ip, _ := p.available.Get(intIPs[i])
		ips[i] = ip.(string)
		p.allocated[fmt.Sprintf("%s-%d", key, i)] = ips[i]
		p.available.Remove(intIPs[i])
	}

	RecordAllocateGlobalIPs(p.cidr, num)

	return ips
}

func (p *IPPool) ReleaseIPBlock(blockKey string, num int) {
	p.Lock()
	defer p.Unlock()

	for i := 0; i < num; i++ {
		key := fmt.Sprintf("%s-%d", blockKey, i)
		ip := p.allocated[key]
		if ip != "" {
			p.available.Put(StringIPToInt(ip), ip)
			delete(p.allocated, key)
		}
	}
	RecordDeallocateGlobalIPs(p.cidr, num)
}
