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

package controller_test

import (
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/event/testing"
	v1meta "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	testHandlerName = "test-handler"
)

var _ = Describe("Event controller", func() {
	t := newTestDriver()

	When("a Node is created, updated and deleted", func() {
		It("should correctly notify the handler", func() {
			node := t.CreateNode(testing.NewNode(t.Hostname))

			t.awaitEvent(testing.EvNodeCreated, node)

			node.Labels = map[string]string{"labeled-i-am": "i-am"}
			t.UpdateNode(node)

			t.awaitEvent(testing.EvNodeUpdated, node)

			t.DeleteNode(node.GetName())

			t.awaitEvent(testing.EvNodeRemoved, node)
			t.ensureNoEvents()
		})
	})

	When("a local Endpoint on this host is created, updated and deleted", func() {
		It("should correctly notify the handler", func() {
			t.testLocalEndpoint()
		})
	})

	When("remote Endpoints are created, updated and deleted", func() {
		It("should correctly notify the handler", func() {
			t.testRemoteEndpoints()
		})
	})

	When("the handler returns an error from an event notification", func() {
		It("should retry the event", func() {
			t.handler.FailOnEvent(testing.EvLocalEndpointCreated, testing.EvLocalEndpointUpdated, testing.EvLocalEndpointRemoved,
				testing.EvTransitionToGateway, testing.EvTransitionToNonGateway, testing.EvRemoteEndpointCreated,
				testing.EvRemoteEndpointUpdated, testing.EvRemoteEndpointRemoved)

			t.testLocalEndpoint()
			t.testRemoteEndpoints()
		})
	})

	Context("with multiple event handlers", func() {
		const (
			allEventsHandler     = "all-events"
			endpointsOnlyHandler = "endpoints-only"
		)

		var handler2Events, endpointsOnlyHandlerEvents chan testing.TestEvent

		BeforeEach(func() {
			handler2Events = make(chan testing.TestEvent, 1000)
			endpointsOnlyHandlerEvents = make(chan testing.TestEvent, 1000)

			t.handlers = []event.Handler{
				t.handler,
				testing.NewTestHandler(allEventsHandler, event.AnyNetworkPlugin, handler2Events),
				testing.NewEndpointsOnlyHandler(endpointsOnlyHandler, event.AnyNetworkPlugin, endpointsOnlyHandlerEvents),
			}
		})

		It("should correctly notify all handlers", func() {
			By("Create local Endpoint")

			endpoint := t.CreateLocalHostEndpoint()

			t.awaitEvent(testing.EvLocalEndpointCreated, endpoint)
			t.awaitEvent(testing.EvTransitionToGateway, nil)

			awaitEvent(allEventsHandler, testing.EvLocalEndpointCreated, endpoint, handler2Events)
			awaitEvent(allEventsHandler, testing.EvTransitionToGateway, nil, handler2Events)

			awaitEvent(endpointsOnlyHandler, testing.EvLocalEndpointCreated, endpoint, endpointsOnlyHandlerEvents)
			awaitEvent(endpointsOnlyHandler, testing.EvTransitionToGateway, nil, endpointsOnlyHandlerEvents)

			By("Create node")

			node := t.CreateNode(testing.NewNode(t.Hostname))

			t.awaitEvent(testing.EvNodeCreated, node)
			awaitEvent(allEventsHandler, testing.EvNodeCreated, node, handler2Events)
			Consistently(endpointsOnlyHandlerEvents).ShouldNot(Receive())
		})
	})

	When("multiple remote Endpoints for the same cluster are created and removed", func() {
		It("should only notify the handler of the latest Endpoint", func() {
			now := time.Now()
			aFewSecondsLater := now.Add(2 * time.Second)

			latestEndpoint := t.CreateEndpoint(&submV1.Endpoint{
				ObjectMeta: v1meta.ObjectMeta{Name: "latest", CreationTimestamp: v1meta.NewTime(aFewSecondsLater)},
				Spec:       submV1.EndpointSpec{ClusterID: "east"},
			})
			t.awaitEvent(testing.EvRemoteEndpointCreated, latestEndpoint)

			staleEndpoint := t.CreateEndpoint(&submV1.Endpoint{
				ObjectMeta: v1meta.ObjectMeta{Name: "stale", CreationTimestamp: v1meta.NewTime(now)},
				Spec:       submV1.EndpointSpec{ClusterID: "east"},
			})
			t.ensureNoEvents()

			t.DeleteEndpoint(staleEndpoint.GetName())
			t.ensureNoEvents()

			t.DeleteEndpoint(latestEndpoint.GetName())
			t.awaitEvent(testing.EvRemoteEndpointRemoved, latestEndpoint)

			t.CreateEndpoint(staleEndpoint)
			t.awaitEvent(testing.EvRemoteEndpointCreated, staleEndpoint)
		})
	})
})

type testDriver struct {
	*testing.ControllerSupport
	testEvents chan testing.TestEvent
	handler    *TestHandler
	handlers   []event.Handler
}

func newTestDriver() *testDriver {
	t := &testDriver{
		ControllerSupport: testing.NewControllerSupport(),
	}

	BeforeEach(func() {
		t.testEvents = make(chan testing.TestEvent, 1000)
		t.handler = &TestHandler{
			TestHandler: testing.NewTestHandler(testHandlerName, event.AnyNetworkPlugin, t.testEvents),
		}
		t.handlers = []event.Handler{t.handler}
	})

	JustBeforeEach(func() {
		t.Start(t.handlers...)
	})

	return t
}

func (t *testDriver) awaitEvent(name string, param interface{}) {
	awaitEvent(testHandlerName, name, param, t.testEvents)
}

func awaitEvent(handler, name string, param interface{}, eventCh chan testing.TestEvent) {
	Eventually(eventCh).Should(Receive(Equal(
		testing.TestEvent{Handler: handler, Name: name, Parameter: param})))
}

func (t *testDriver) ensureNoEvents() {
	Consistently(t.testEvents).ShouldNot(Receive())
}

func (t *testDriver) testLocalEndpoint() {
	By("Create local Endpoint")

	endpoint := t.CreateLocalHostEndpoint()

	t.awaitEvent(testing.EvLocalEndpointCreated, endpoint)
	t.awaitEvent(testing.EvTransitionToGateway, nil)

	t.CreateLocalHostEndpoint()
	Consistently(t.testEvents).ShouldNot(Receive(Equal(
		testing.TestEvent{Handler: testHandlerName, Name: testing.EvTransitionToGateway})))

	By("Update local Endpoint")

	endpoint.Labels = map[string]string{"labeled-i-am": "i-am"}
	t.UpdateEndpoint(endpoint)

	t.awaitEvent(testing.EvLocalEndpointUpdated, endpoint)

	By("Delete local Endpoint")

	t.DeleteEndpoint(endpoint.GetName())

	t.awaitEvent(testing.EvLocalEndpointRemoved, endpoint)
	t.awaitEvent(testing.EvTransitionToNonGateway, nil)
	t.ensureNoEvents()
}

func (t *testDriver) testRemoteEndpoints() {
	By("Create first remote Endpoint")

	endpoint1 := t.CreateEndpoint(testing.NewEndpoint("remote-cluster1", "host"))

	t.awaitEvent(testing.EvRemoteEndpointCreated, endpoint1)
	Expect(t.handler.remoteEndpoints.Load()).To(Equal([]submV1.Endpoint{*endpoint1}))

	By("Create second remote Endpoint")

	endpoint2 := t.CreateEndpoint(testing.NewEndpoint("remote-cluster2", "host"))

	t.awaitEvent(testing.EvRemoteEndpointCreated, endpoint2)
	Expect(t.handler.remoteEndpoints.Load()).To(ContainElements(*endpoint1, *endpoint2))

	By("Update first remote Endpoint")

	endpoint1.Labels = map[string]string{"labeled-i-am": "i-am"}
	t.UpdateEndpoint(endpoint1)

	t.awaitEvent(testing.EvRemoteEndpointUpdated, endpoint1)

	By("Delete second remote Endpoint")

	t.DeleteEndpoint(endpoint2.GetName())

	t.awaitEvent(testing.EvRemoteEndpointRemoved, endpoint2)
	Expect(t.handler.remoteEndpoints.Load()).To(Equal([]submV1.Endpoint{*endpoint1}))

	By("Delete first remote Endpoint")

	t.DeleteEndpoint(endpoint1.GetName())

	t.awaitEvent(testing.EvRemoteEndpointRemoved, endpoint1)
	Expect(t.handler.remoteEndpoints.Load()).To(BeEmpty())
	t.ensureNoEvents()
}

type TestHandler struct {
	*testing.TestHandler
	remoteEndpoints atomic.Value
}

func (t *TestHandler) LocalEndpointCreated(endpoint *submV1.Endpoint) error {
	defer GinkgoRecover()
	Expect(t.State().IsOnGateway()).To(BeTrue())

	return t.TestHandler.LocalEndpointCreated(endpoint)
}

func (t *TestHandler) LocalEndpointRemoved(endpoint *submV1.Endpoint) error {
	defer GinkgoRecover()
	Expect(t.State().IsOnGateway()).To(BeFalse())

	return t.TestHandler.LocalEndpointRemoved(endpoint)
}

func (t *TestHandler) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	t.storeRemoteEndpoints()
	return t.TestHandler.RemoteEndpointCreated(endpoint)
}

func (t *TestHandler) RemoteEndpointRemoved(endpoint *submV1.Endpoint) error {
	t.storeRemoteEndpoints()
	return t.TestHandler.RemoteEndpointRemoved(endpoint)
}

func (t *TestHandler) storeRemoteEndpoints() {
	eps := t.State().GetRemoteEndpoints()
	if eps == nil {
		eps = []submV1.Endpoint{}
	}

	t.remoteEndpoints.Store(eps)
}
