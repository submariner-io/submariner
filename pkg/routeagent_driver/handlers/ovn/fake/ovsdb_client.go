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
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	. "github.com/onsi/gomega"
	"github.com/ovn-org/libovsdb/cache"
	libovsdbclient "github.com/ovn-org/libovsdb/client"
	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/nbdb"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/slices"
)

type OVSDBClient struct {
	mutex     sync.Mutex
	connected atomic.Bool
	models    map[reflect.Type][]any
}

func NewOVSDBClient() *OVSDBClient {
	return &OVSDBClient{
		models: map[reflect.Type][]any{},
	}
}

func (c *OVSDBClient) Connect(_ context.Context) error {
	c.connected.Store(true)
	return nil
}

func (c *OVSDBClient) Disconnect() {
	c.connected.Store(false)
}

func (c *OVSDBClient) Close() {
}

func (c *OVSDBClient) Schema() ovsdb.DatabaseSchema {
	return ovsdb.DatabaseSchema{}
}

func (c *OVSDBClient) Cache() *cache.TableCache {
	return nil
}

func (c *OVSDBClient) SetOption(_ libovsdbclient.Option) error {
	return nil
}

func (c *OVSDBClient) Connected() bool {
	return c.connected.Load()
}

func (c *OVSDBClient) DisconnectNotify() chan struct{} {
	return make(chan struct{})
}

func (c *OVSDBClient) Echo(_ context.Context) error {
	return nil
}

func (c *OVSDBClient) Transact(_ context.Context, _ ...ovsdb.Operation) ([]ovsdb.OperationResult, error) {
	return []ovsdb.OperationResult{}, nil
}

func (c *OVSDBClient) Monitor(_ context.Context, _ *libovsdbclient.Monitor) (libovsdbclient.MonitorCookie, error) {
	return libovsdbclient.MonitorCookie{}, nil
}

func (c *OVSDBClient) MonitorAll(_ context.Context) (libovsdbclient.MonitorCookie, error) {
	return libovsdbclient.MonitorCookie{}, nil
}

func (c *OVSDBClient) MonitorCancel(_ context.Context, _ libovsdbclient.MonitorCookie) error {
	return nil
}

func (c *OVSDBClient) NewMonitor(_ ...libovsdbclient.MonitorOption) *libovsdbclient.Monitor {
	return &libovsdbclient.Monitor{
		Method:            ovsdb.ConditionalMonitorSinceRPC,
		Errors:            make([]error, 0),
		LastTransactionID: "00000000-0000-0000-0000-000000000000",
	}
}

func (c *OVSDBClient) CurrentEndpoint() string {
	return ""
}

func (c *OVSDBClient) List(_ context.Context, _ interface{}) error {
	return nil
}

func (c *OVSDBClient) WhereCache(predicate interface{}) libovsdbclient.ConditionalAPI {
	return &predicateConditionalAPI{client: c, predicate: predicate}
}

func (c *OVSDBClient) Where(m model.Model, _ ...model.Condition) libovsdbclient.ConditionalAPI {
	return &modelConditionalAPI{client: c, model: m}
}

func (c *OVSDBClient) WhereAll(_ model.Model, _ ...model.Condition) libovsdbclient.ConditionalAPI {
	return &noopConditionalAPI{}
}

func (c *OVSDBClient) Get(_ context.Context, _ model.Model) error {
	return nil
}

func (c *OVSDBClient) Create(models ...model.Model) ([]ovsdb.Operation, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	for _, m := range models {
		c.models[reflect.TypeOf(m)] = append(c.models[reflect.TypeOf(m)], m)
	}

	return []ovsdb.Operation{}, nil
}

func (c *OVSDBClient) hasModel(m any) bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	for _, o := range c.models[reflect.TypeOf(m)] {
		switch t := m.(type) {
		case *nbdb.LogicalRouterPolicy:
			if strings.Contains(o.(*nbdb.LogicalRouterPolicy).Match, t.Match) &&
				reflect.DeepEqual(o.(*nbdb.LogicalRouterPolicy).Nexthop, t.Nexthop) {
				return true
			}
		case *nbdb.LogicalRouterStaticRoute:
			if o.(*nbdb.LogicalRouterStaticRoute).IPPrefix == t.IPPrefix {
				return true
			}
		}
	}

	return false
}

func (c *OVSDBClient) AwaitModel(m any) {
	Eventually(func() bool {
		return c.hasModel(m)
	}).Should(BeTrue(), "OVSBD model not found: %s", resource.ToJSON(m))
}

func (c *OVSDBClient) AwaitNoModel(m any) {
	Eventually(func() bool {
		return c.hasModel(m)
	}).Should(BeFalse(), "OVSBD model exists: %s", resource.ToJSON(m))
}

type noopConditionalAPI struct{}

func (c noopConditionalAPI) List(_ context.Context, _ interface{}) error {
	return nil
}

func (c noopConditionalAPI) Mutate(_ model.Model, _ ...model.Mutation) ([]ovsdb.Operation, error) {
	return []ovsdb.Operation{}, nil
}

func (c noopConditionalAPI) Update(_ model.Model, _ ...interface{}) ([]ovsdb.Operation, error) {
	return []ovsdb.Operation{}, nil
}

func (c noopConditionalAPI) Delete() ([]ovsdb.Operation, error) {
	return []ovsdb.Operation{}, nil
}

func (c noopConditionalAPI) Wait(_ ovsdb.WaitCondition, _ *int, _ model.Model, _ ...interface{}) ([]ovsdb.Operation, error) {
	return []ovsdb.Operation{}, nil
}

type predicateConditionalAPI struct {
	noopConditionalAPI
	client    *OVSDBClient
	predicate any
}

func list[T any](client *OVSDBClient, t T, p any, result *[]T) {
	client.mutex.Lock()
	defer client.mutex.Unlock()

	r := []T{}
	fn := reflect.ValueOf(p)

	for _, o := range client.models[reflect.TypeOf(t)] {
		v := fn.Call([]reflect.Value{reflect.ValueOf(o)})
		if v[0].Bool() {
			r = append(r, o.(T))
		}
	}

	*result = r
}

func (c *predicateConditionalAPI) List(_ context.Context, result interface{}) error {
	switch r := result.(type) {
	case *[]*nbdb.LogicalRouter:
		list(c.client, &nbdb.LogicalRouter{}, c.predicate, r)
	case *[]*nbdb.LogicalRouterPolicy:
		list(c.client, &nbdb.LogicalRouterPolicy{}, c.predicate, r)
	case *[]*nbdb.LogicalRouterStaticRoute:
		list(c.client, &nbdb.LogicalRouterStaticRoute{}, c.predicate, r)
	}

	return nil
}

type modelConditionalAPI struct {
	noopConditionalAPI
	client *OVSDBClient
	model  any
}

func (c *modelConditionalAPI) Delete() ([]ovsdb.Operation, error) {
	c.client.mutex.Lock()
	defer c.client.mutex.Unlock()

	c.client.models[reflect.TypeOf(c.model)], _ = slices.Remove(c.client.models[reflect.TypeOf(c.model)], c.model, resource.ToJSON)

	return []ovsdb.Operation{}, nil
}
