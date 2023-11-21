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

package event_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/event/logger"
	"github.com/submariner-io/submariner/pkg/event/testing"
	k8sV1 "k8s.io/api/core/v1"
	v1meta "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const npGenericKubeproxyIptables = "GenericKubeproxyIptables"

var _ = Describe("Event Registry", func() {
	When("handlers with various network plugins are added to the registry", func() {
		var (
			matchingHandlers    []*testing.TestHandler
			nonMatchingHandlers []*testing.TestHandler
			registry            *event.Registry
			allTestEvents       chan testing.TestEvent
		)

		BeforeEach(func() {
			allTestEvents = make(chan testing.TestEvent, 1000)

			nonMatchingHandlers = []*testing.TestHandler{
				testing.NewTestHandler("ovn-handler", cni.OVNKubernetes, allTestEvents),
			}

			matchingHandlers = []*testing.TestHandler{
				testing.NewTestHandler("kubeproxy-handler1", npGenericKubeproxyIptables, allTestEvents),
				testing.NewTestHandler("kubeproxy-handler2", npGenericKubeproxyIptables, allTestEvents),
				testing.NewTestHandler("wildcard-handler", event.AnyNetworkPlugin, allTestEvents),
			}

			var err error

			registry, err = event.NewRegistry("test-registry", npGenericKubeproxyIptables, logger.NewHandler(), matchingHandlers[0],
				nonMatchingHandlers[0], matchingHandlers[1], matchingHandlers[2])
			Expect(err).NotTo(HaveOccurred())
		})

		It("should initialize all matching handlers", func() {
			for _, h := range matchingHandlers {
				Expect(h.Initialized).To(BeTrue())
			}

			for _, h := range nonMatchingHandlers {
				Expect(h.Initialized).To(BeFalse())
			}
		})

		It("should invoke all matching handlers of all events in registration order", func() {
			for ev, f := range allEvents(registry) {
				err := f()
				Expect(err).To(Succeed())

				for _, h := range matchingHandlers {
					ev.Handler = h.Name
					Expect(allTestEvents).To(Receive(Equal(ev)))
				}
			}

			Expect(allTestEvents).ToNot(Receive())
		})

		When("one handler returns an error", func() {
			It("should invoke subsequent matching handlers", func() {
				events := allEvents(registry)
				for ev := range events {
					matchingHandlers[0].FailOnEvent(ev.Name)
				}

				for ev, f := range events {
					err := f()
					Expect(err).To(HaveOccurred())

					for i := 1; i < len(matchingHandlers); i++ {
						ev.Handler = matchingHandlers[i].Name
						Expect(allTestEvents).To(Receive(Equal(ev)))
					}
				}
			})
		})

		When("the RemoteEndpointCreated notification is fired out of order", func() {
			It("should skip processing the stale event", func() {
				now := time.Now()
				aFewSecondsLater := now.Add(2 * time.Second)
				staleEndpoint := &submV1.Endpoint{
					ObjectMeta: v1meta.ObjectMeta{Name: "endpoint1", CreationTimestamp: v1meta.NewTime(now)},
					Spec:       submV1.EndpointSpec{ClusterID: "eastCluster"},
				}
				latestEndpoint := &submV1.Endpoint{
					ObjectMeta: v1meta.ObjectMeta{Name: "endpoint1", CreationTimestamp: v1meta.NewTime(aFewSecondsLater)},
					Spec:       submV1.EndpointSpec{ClusterID: "eastCluster"},
				}

				event := testing.TestEvent{
					Name:      testing.EvRemoteEndpointCreated,
					Parameter: latestEndpoint,
				}

				err := registry.RemoteEndpointCreated(latestEndpoint)
				Expect(err).To(Succeed())

				for _, h := range matchingHandlers {
					event.Handler = h.Name
					Expect(allTestEvents).To(Receive(Equal(event)))
				}

				err = registry.RemoteEndpointCreated(staleEndpoint)
				Expect(err).To(Succeed())

				for range matchingHandlers {
					Expect(allTestEvents).NotTo(Receive(Equal(event)))
				}
			})
		})
	})

	When("SetHandlerState is called on the registry", func() {
		It("should invoke SetState on the handlers", func() {
			h := testing.NewTestHandler("test", event.AnyNetworkPlugin, nil)
			registry, err := event.NewRegistry("test-registry", event.AnyNetworkPlugin, h)
			Expect(err).NotTo(HaveOccurred())

			registry.SetHandlerState(&testing.TestHandlerState{Gateway: true})
			Expect(h.State().IsOnGateway()).To(BeTrue())
		})
	})

	When("SetState has not been called for a handler", func() {
		Specify("State should return a valid instance", func() {
			h := testing.NewTestHandler("test", event.AnyNetworkPlugin, nil)
			hc := h.State()
			Expect(hc).ToNot(BeNil())
		})
	})
})

func allEvents(registry *event.Registry) map[testing.TestEvent]func() error {
	endpoint := &submV1.Endpoint{ObjectMeta: v1meta.ObjectMeta{Name: "endpoint1"}}
	node := &k8sV1.Node{ObjectMeta: v1meta.ObjectMeta{Name: "node1"}}

	return map[testing.TestEvent]func() error{
		{Name: testing.EvStop}:                                       func() error { return registry.StopHandlers() },
		{Name: testing.EvUninstall}:                                  func() error { return registry.Uninstall() },
		{Name: testing.EvTransitionToGateway}:                        registry.TransitionToGateway,
		{Name: testing.EvTransitionToNonGateway}:                     registry.TransitionToNonGateway,
		{Name: testing.EvNodeCreated, Parameter: node}:               func() error { return registry.NodeCreated(node) },
		{Name: testing.EvNodeUpdated, Parameter: node}:               func() error { return registry.NodeUpdated(node) },
		{Name: testing.EvNodeRemoved, Parameter: node}:               func() error { return registry.NodeRemoved(node) },
		{Name: testing.EvLocalEndpointCreated, Parameter: endpoint}:  func() error { return registry.LocalEndpointCreated(endpoint) },
		{Name: testing.EvLocalEndpointUpdated, Parameter: endpoint}:  func() error { return registry.LocalEndpointUpdated(endpoint) },
		{Name: testing.EvLocalEndpointRemoved, Parameter: endpoint}:  func() error { return registry.LocalEndpointRemoved(endpoint) },
		{Name: testing.EvRemoteEndpointCreated, Parameter: endpoint}: func() error { return registry.RemoteEndpointCreated(endpoint) },
		{Name: testing.EvRemoteEndpointUpdated, Parameter: endpoint}: func() error { return registry.RemoteEndpointUpdated(endpoint) },
		{Name: testing.EvRemoteEndpointRemoved, Parameter: endpoint}: func() error { return registry.RemoteEndpointRemoved(endpoint) },
	}
}
