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

	"github.com/emirpasic/gods/maps/treemap"
)

type IPPool struct {
	cidr      string
	net       *net.IPNet
	size      int
	available *treemap.Map // IntIP is the key, StringIP is value
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
		return nil, fmt.Errorf("invalid prefix for CIDR %q", cidr)
	}

	pool := &IPPool{
		cidr:      cidr,
		net:       network,
		size:      size,
		available: treemap.NewWithIntComparator(),
	}
	startingIP := ipToInt(pool.net.IP) + 1

	for i := 0; i < pool.size; i++ {
		intIP := startingIP + i
		ip := intToIP(intIP).String()
		pool.available.Put(intIP, ip)
	}

	// TODO: RecordAvailability(cidr, size)

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

func (p *IPPool) allocate() (string, error) {
	p.Lock()
	defer p.Unlock()

	iter := p.available.Iterator()
	if !p.available.Empty() {
		iter.Last()
		ip := iter.Value().(string)
		p.available.Remove(iter.Key())
		// TODO: RecordAllocateGlobalIP(p.cidr)

		return ip, nil
	}

	return "", errors.New("insufficient IPs available for allocation")
}

func (p *IPPool) Allocate(num int) ([]string, error) {
	if p.available.Size() < num {
		return nil, errors.New("insufficient IPs available for allocation")
	}

	ips := make([]string, num)

	if num == 1 {
		ip, err := p.allocate()
		if err != nil {
			return nil, err
		}

		ips[0] = ip

		return ips, nil
	}

	intIPs := make([]int, num)
	prev, current := 0, 0

	p.Lock()
	defer p.Unlock()

	iter := p.available.Iterator()
	for iter.Next() {
		if current == num {
			return p.allocateBlock(intIPs), nil
		}

		intIP := iter.Key().(int)
		intIPs[current] = intIP
		if current == 0 {
			current++
			continue
		}

		prevIntIP := intIPs[prev]
		if prevIntIP+1 != intIP {
			prev, current = 0, 0
		} else {
			prev = current
			current++
		}
	}

	return nil, fmt.Errorf("unable to find contiguous block of %d IPs for allocation", num)
}

func (p *IPPool) Release(ips ...string) {
	p.Lock()
	defer p.Unlock()

	for _, ip := range ips {
		p.available.Put(StringIPToInt(ip), ip)
	}
}

func (p *IPPool) Reserve(ips ...string) error {
	num := len(ips)
	intIPs := make([]int, num)

	p.Lock()
	defer p.Unlock()

	for i := 0; i < num; i++ {
		intIPs[i] = StringIPToInt(ips[i])
		_, found := p.available.Get(intIPs[i])
		if !found {
			return fmt.Errorf("requested IP %s already allocated", ips[i])
		}
	}

	for i := 0; i < num; i++ {
		p.available.Remove(intIPs[i])
	}

	return nil
}

func (p *IPPool) IsAvailable(ip string) bool {
	p.RLock()
	_, result := p.available.Get(StringIPToInt(ip))
	p.RUnlock()

	return result
}

// Make sure Lock is already taken before calling this
func (p *IPPool) allocateBlock(intIPs []int) []string {
	num := len(intIPs)
	ips := make([]string, num)

	for i := 0; i < num; i++ {
		ip, _ := p.available.Get(intIPs[i])
		ips[i] = ip.(string)

		p.available.Remove(intIPs[i])
	}

	// TODO: RecordAllocateGlobalIPs(p.cidr, num)

	return ips
}
