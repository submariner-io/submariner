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
	network   *net.IPNet
	size      int
	available *treemap.Map // int IP is the key, string IP is the value
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
		network:   network,
		size:      size,
		available: treemap.NewWithIntComparator(),
	}

	startingIP := ipToInt(pool.network.IP) + 1

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

func (p *IPPool) allocateOne() ([]string, error) {
	p.Lock()
	defer p.Unlock()

	iter := p.available.Iterator()
	if iter.Last() {
		p.available.Remove(iter.Key())
		// TODO: RecordAllocateGlobalIP(p.cidr)

		return []string{iter.Value().(string)}, nil
	}

	return nil, errors.New("insufficient IPs available for allocation")
}

func (p *IPPool) Allocate(num int) ([]string, error) {
	switch {
	case num < 0:
		return nil, errors.New("the number to allocate cannot be negative")
	case num == 0:
		return []string{}, nil
	case num == 1:
		return p.allocateOne()
	}

	if p.available.Size() < num {
		return nil, fmt.Errorf("insufficient IPs available (%d) to allocate %d", p.available.Size(), num)
	}

	retIPs := make([]string, num)

	var prevIntIP, firstIntIP, current int

	p.Lock()
	defer p.Unlock()

	iter := p.available.Iterator()
	for iter.Next() {
		intIP := iter.Key().(int)
		retIPs[current] = iter.Value().(string)

		if current == 0 || prevIntIP+1 != intIP {
			firstIntIP = intIP
			prevIntIP = intIP
			retIPs[0] = retIPs[current]
			current = 1

			continue
		}

		prevIntIP = intIP
		current++

		if current == num {
			for i := 0; i < num; i++ {
				p.available.Remove(firstIntIP)
				firstIntIP++
			}

			// TODO: RecordAllocateGlobalIPs(p.cidr, num)

			return retIPs, nil
		}
	}

	return nil, fmt.Errorf("unable to allocate a contiguous block of %d IPs - available pool size is %d",
		num, p.available.Size())
}

func (p *IPPool) Release(ips ...string) error {
	p.Lock()
	defer p.Unlock()

	for _, ip := range ips {
		if !p.network.Contains(net.ParseIP(ip)) {
			return fmt.Errorf("released IP %s is not contained in CIDR %s", ip, p.cidr)
		}

		p.available.Put(StringIPToInt(ip), ip)
	}

	return nil
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
			if !p.network.Contains(net.ParseIP(ips[i])) {
				return fmt.Errorf("the requested IP %s is not contained in CIDR %s", ips[i], p.cidr)
			}

			return fmt.Errorf("the requested IP %s is already allocated", ips[i])
		}
	}

	for i := 0; i < num; i++ {
		p.available.Remove(intIPs[i])
	}

	return nil
}

func (p *IPPool) Size() int {
	p.RLock()
	defer p.RUnlock()

	return p.available.Size()
}
