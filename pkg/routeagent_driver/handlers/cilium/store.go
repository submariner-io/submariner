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

package cilium

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/pkg/errors"
)

// RouteStore persists Cilium ClusterMesh-compatible keys for remote CIDRs.
type RouteStore interface {
	Bootstrap(ctx context.Context, remoteName string, clusterID uint32) error
	UpsertRoute(ctx context.Context, cidrStr, hostIP string, clusterID uint32) error
	DeleteRoute(ctx context.Context, cidrStr string) error
	DeleteClusterConfig(ctx context.Context, remoteName string) error
	// TouchHeartbeat updates cilium/.heartbeat so watching Cilium agents see a live kvstore.
	TouchHeartbeat(ctx context.Context) error
	Close() error
}

// NewMemoryRouteStore returns an in-process store for unit tests.
func NewMemoryRouteStore() RouteStore {
	return newMemoryStore()
}

// memoryStore is an in-process store used by unit tests (and injectable fakes).
type memoryStore struct {
	mu        sync.Mutex
	config    map[string][]byte
	routes    map[string][]byte
	heartbeat []byte
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		config: map[string][]byte{},
		routes: map[string][]byte{},
	}
}

func (s *memoryStore) Bootstrap(_ context.Context, remoteName string, clusterID uint32) error {
	b, err := marshalClusterConfig(defaultClusterConfig(clusterID))
	if err != nil {
		return errors.Wrap(err, "marshal cluster-config")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.config[clusterConfigKey(remoteName)] = b

	return nil
}

func (s *memoryStore) UpsertRoute(_ context.Context, cidrStr, hostIP string, clusterID uint32) error {
	pair, key, err := buildIPIdentityPair(cidrStr, hostIP, clusterID)
	if err != nil {
		return err
	}

	b, err := marshalIPIdentityPair(pair)
	if err != nil {
		return errors.Wrap(err, "marshal IPIdentityPair")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.routes[key] = b

	return nil
}

func (s *memoryStore) DeleteRoute(_ context.Context, cidrStr string) error {
	ip, mask, err := parseCIDR(cidrStr)
	if err != nil {
		return errors.Wrap(err, "parse CIDR")
	}

	key := ipIdentityKey(prefixString(&ipIdentityPair{IP: ip, Mask: mask}))

	s.mu.Lock()

	defer s.mu.Unlock()

	delete(s.routes, key)

	return nil
}

func (s *memoryStore) DeleteClusterConfig(_ context.Context, remoteName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.config, clusterConfigKey(remoteName))

	return nil
}

func (s *memoryStore) TouchHeartbeat(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.heartbeat = []byte(time.Now().UTC().Format(time.RFC3339))

	return nil
}

func (s *memoryStore) Close() error {
	return nil
}

func (s *memoryStore) getHeartbeat() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return string(s.heartbeat)
}

func (s *memoryStore) getRoute(cidrStr string) []byte {
	ip, mask, err := parseCIDR(cidrStr)
	if err != nil {
		return nil
	}

	key := ipIdentityKey(prefixString(&ipIdentityPair{IP: ip, Mask: mask}))

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.routes[key]
}

func (s *memoryStore) routeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.routes)
}

func buildIPIdentityPair(cidrStr, hostIP string, clusterID uint32) (*ipIdentityPair, string, error) {
	ip, mask, err := parseCIDR(cidrStr)
	if err != nil {
		return nil, "", errors.Wrap(err, "parse CIDR")
	}

	hip := net.ParseIP(hostIP)
	if hip == nil {
		return nil, "", errors.Errorf("invalid hostIP %q", hostIP)
	}

	if v4 := hip.To4(); v4 != nil {
		hip = v4
	}

	pair := &ipIdentityPair{
		IP:     ip,
		Mask:   mask,
		HostIP: hip,
		ID:     identityForCluster(clusterID, defaultRemoteIdentityLocalID),
		Key:    0,
	}

	return pair, ipIdentityKey(prefixString(pair)), nil
}
