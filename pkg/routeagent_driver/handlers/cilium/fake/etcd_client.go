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

package fake

import (
	"context"
	"sync"

	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// EtcdClient is an in-memory stand-in for clientv3.Client.
type EtcdClient struct {
	mutex  sync.Mutex
	kvs    map[string][]byte
	putErr error
	closed bool
}

// NewEtcdClient returns an empty fake etcd KV client.
func NewEtcdClient() *EtcdClient {
	return &EtcdClient{kvs: map[string][]byte{}}
}

// SetPutError makes subsequent Put calls return err (nil clears the fault).
func (c *EtcdClient) SetPutError(err error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.putErr = err
}

// Value returns a copy of the value for key, or nil if missing.
func (c *EtcdClient) Value(key string) []byte {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	v, ok := c.kvs[key]
	if !ok {
		return nil
	}

	out := make([]byte, len(v))
	copy(out, v)

	return out
}

// KeyCount returns the number of stored keys.
func (c *EtcdClient) KeyCount() int {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	return len(c.kvs)
}

// KeyCountWithPrefix returns the number of keys with the given prefix.
func (c *EtcdClient) KeyCountWithPrefix(prefix string) int {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	n := 0

	for k := range c.kvs {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			n++
		}
	}

	return n
}

// HasKey reports whether key exists.
func (c *EtcdClient) HasKey(key string) bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	_, ok := c.kvs[key]

	return ok
}

// Closed reports whether Close was called.
func (c *EtcdClient) Closed() bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	return c.closed
}

func (c *EtcdClient) Put(_ context.Context, key, val string, _ ...clientv3.OpOption) (*clientv3.PutResponse, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.putErr != nil {
		return nil, c.putErr
	}

	c.kvs[key] = []byte(val)

	return &clientv3.PutResponse{}, nil
}

func (c *EtcdClient) Get(_ context.Context, key string, _ ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	v, ok := c.kvs[key]
	if !ok {
		return &clientv3.GetResponse{}, nil
	}

	return &clientv3.GetResponse{
		Kvs: []*mvccpb.KeyValue{{
			Key:   []byte(key),
			Value: append([]byte(nil), v...),
		}},
	}, nil
}

func (c *EtcdClient) Delete(_ context.Context, key string, _ ...clientv3.OpOption) (*clientv3.DeleteResponse, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	delete(c.kvs, key)

	return &clientv3.DeleteResponse{}, nil
}

func (c *EtcdClient) Close() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.closed = true

	return nil
}
