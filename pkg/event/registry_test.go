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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	submv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cni"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/event/logger"
	"github.com/submariner-io/submariner/pkg/event/testing"
	corev1 "k8s.io/api/core/v1"
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
			Expect(registry.GetName()).To(Equal("test-registry"))
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

		Specify("GetHandlers should return all the handlers", func() {
			handlers := registry.GetHandlers()
			Expect(handlers).To(ContainElements(matchingHandlers))
		})
	})

	When("a handler's Init fails", func() {
		It("NewRegistry should return an error", func() {
			h := testing.NewTestHandler("test-handler", event.AnyNetworkPlugin, make(chan testing.TestEvent, 10))
			h.FailOnEvent(testing.EvInit)

			_, err := event.NewRegistry("test-registry", event.AnyNetworkPlugin, h)
			Expect(err).To(HaveOccurred())
		})
	})
})

var _ = Describe("Event Handler", func() {
	When("SetState has not been called", func() {
		Specify("State should return a default instance", func() {
			h := testing.NewTestHandler("test", event.AnyNetworkPlugin, nil)
			state := h.State()
			Expect(state).ToNot(BeNil())
			Expect(state.IsOnGateway()).To(BeFalse())
			Expect(state.GetRemoteEndpoints()).To(BeEmpty())
		})
	})

	When("SetState has been called", func() {
		Specify("State should return the instance", func() {
			h := testing.NewTestHandler("test", event.AnyNetworkPlugin, nil)
			state := &testing.TestHandlerState{}
			h.SetState(state)
			Expect(h.State()).To(BeIdenticalTo(state))
		})
	})

	Specify("HandlerBase should stub all Endpoint methods", func() {
		h := event.HandlerBase{}
		Expect(h.LocalEndpointCreated(&submv1.Endpoint{})).To(Succeed())
		Expect(h.LocalEndpointUpdated(&submv1.Endpoint{})).To(Succeed())
		Expect(h.LocalEndpointRemoved(&submv1.Endpoint{})).To(Succeed())
		Expect(h.RemoteEndpointCreated(&submv1.Endpoint{})).To(Succeed())
		Expect(h.RemoteEndpointUpdated(&submv1.Endpoint{})).To(Succeed())
		Expect(h.RemoteEndpointRemoved(&submv1.Endpoint{})).To(Succeed())
		Expect(h.TransitionToGateway()).To(Succeed())
		Expect(h.TransitionToNonGateway()).To(Succeed())
	})

	Specify("NodeHandlerBase should stub all Node methods", func() {
		h := event.NodeHandlerBase{}
		Expect(h.NodeCreated(&corev1.Node{})).To(Succeed())
		Expect(h.NodeUpdated(&corev1.Node{})).To(Succeed())
		Expect(h.NodeRemoved(&corev1.Node{})).To(Succeed())
	})
})

func allEvents(registry *event.Registry) map[testing.TestEvent]func() error {
	return map[testing.TestEvent]func() error{
		{Name: testing.EvStop}:      func() error { return registry.StopHandlers() },
		{Name: testing.EvUninstall}: func() error { return registry.Uninstall() },
	}
}
