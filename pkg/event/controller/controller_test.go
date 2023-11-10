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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/event/testing"
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
})

type testDriver struct {
	*testing.ControllerSupport
	testEvents chan testing.TestEvent
	handler    *TestHandler
}

func newTestDriver() *testDriver {
	t := &testDriver{
		ControllerSupport: testing.NewControllerSupport(),
	}

	BeforeEach(func() {
		t.testEvents = make(chan testing.TestEvent, 1000)
		t.handler = &TestHandler{
			TestHandler: &testing.TestHandler{
				Name:          testHandlerName,
				NetworkPlugin: event.AnyNetworkPlugin,
				Events:        t.testEvents,
			},
		}
	})

	JustBeforeEach(func() {
		t.Start(t.handler)
	})

	return t
}

func (t *testDriver) awaitEvent(name string, param interface{}) {
	Eventually(t.testEvents).Should(Receive(Equal(
		testing.TestEvent{Handler: testHandlerName, Name: name, Parameter: param})))
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
