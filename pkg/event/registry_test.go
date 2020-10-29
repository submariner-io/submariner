package event_test

import (
	"errors"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	k8sV1 "k8s.io/api/core/v1"
	v1meta "k8s.io/apimachinery/pkg/apis/meta/v1"

	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/event/logger"
)

const npOVNKubernetes = "OVNKubernetes"
const npGenericKubeproxyIptables = "GenericKubeproxyIptables"

var _ = Describe("Event Registry", func() {
	When("handlers with different network plugin are added to the registry", func() {
		var (
			ovn, generic, wildcard *TestHandler
			registry               event.Registry
		)

		BeforeEach(func() {
			registry = event.NewRegistry("test-registry", npGenericKubeproxyIptables)
			ovn = NewTestHandler("handler-ovn", npOVNKubernetes)
			generic = NewTestHandler("handler-kubeproxy", npGenericKubeproxyIptables)
			wildcard = NewTestHandler("wildcard-handler", event.AnyNetworkPlugin)
			err := registry.AddHandlers(logger.NewHandler(), ovn, generic, wildcard)
			Expect(err).NotTo(HaveOccurred())
			cleanupTestEvents()
		})

		It("should only initialize the handlers whose network plugin matches that of the registry", func() {
			Expect(ovn.Initialized).To(BeFalse())
			Expect(generic.Initialized).To(BeTrue())
			Expect(wildcard.Initialized).To(BeTrue())
		})

		It("should only invoke handlers whose network plugin matches that of the registry", func() {
			_ = registry.TransitionToGateway()
			Expect(ovn.Events).ShouldNot(Receive())
			Expect(generic.Events).Should(Receive())
			Expect(wildcard.Events).Should(Receive())
		})
	})

	When("handlers with the same network plugin are added to the registry", func() {
		var (
			handler1, handler2 *TestHandler
			registry           event.Registry
		)

		BeforeEach(func() {
			registry = event.NewRegistry("test-registry", npGenericKubeproxyIptables)
			handler1 = NewTestHandler("handler1", npGenericKubeproxyIptables)
			handler2 = NewTestHandler("handler2", npGenericKubeproxyIptables)
			err := registry.AddHandlers(logger.NewHandler(), handler1, handler2)
			Expect(err).NotTo(HaveOccurred())
			cleanupTestEvents()
		})

		It("should initialize all the handlers", func() {
			Expect(handler1.Initialized).To(BeTrue())
			Expect(handler2.Initialized).To(BeTrue())
		})

		It("should invoke all handlers for all events", func() {
			endpoint := &submV1.Endpoint{ObjectMeta: v1meta.ObjectMeta{Name: "endpoint1"}}
			node := &k8sV1.Node{ObjectMeta: v1meta.ObjectMeta{Name: "node1"}}

			events := map[TestEvent]func() error{
				{Name: evStop, Parameter: false}:                     func() error { return registry.StopHandlers(false) },
				{Name: evTransitionToGateway}:                        registry.TransitionToGateway,
				{Name: evTransitionToNonGateway}:                     registry.TransitionToNonGateway,
				{Name: evNodeCreated, Parameter: node}:               func() error { return registry.NodeCreated(node) },
				{Name: evNodeUpdated, Parameter: node}:               func() error { return registry.NodeUpdated(node) },
				{Name: evNodeRemoved, Parameter: node}:               func() error { return registry.NodeRemoved(node) },
				{Name: evLocalEndpointCreated, Parameter: endpoint}:  func() error { return registry.LocalEndpointCreated(endpoint) },
				{Name: evLocalEndpointUpdated, Parameter: endpoint}:  func() error { return registry.LocalEndpointUpdated(endpoint) },
				{Name: evLocalEndpointRemoved, Parameter: endpoint}:  func() error { return registry.LocalEndpointRemoved(endpoint) },
				{Name: evRemoteEndpointCreated, Parameter: endpoint}: func() error { return registry.RemoteEndpointCreated(endpoint) },
				{Name: evRemoteEndpointUpdated, Parameter: endpoint}: func() error { return registry.RemoteEndpointUpdated(endpoint) },
				{Name: evRemoteEndpointRemoved, Parameter: endpoint}: func() error { return registry.RemoteEndpointRemoved(endpoint) },
			}

			for ev, f := range events {
				err := f()
				Expect(err).To(Succeed())
				Expect(handler1.Events).Should(Receive(Equal(ev)))
				Expect(handler2.Events).Should(Receive(Equal(ev)))

				// Handlers should be handled invoked in order of registration
				ev.Handler = handler1.Name
				Expect(allTestEvents).Should(Receive(Equal(ev)))
				ev.Handler = handler2.Name
				Expect(allTestEvents).Should(Receive(Equal(ev)))
			}
		})

		It("one handler returns error subsequent handlers should still execute", func() {
			handler1.FailOnEvent = errors.New("I shall fail!")
			err := registry.TransitionToGateway()
			Expect(handler1.Events).ShouldNot(Receive())
			Expect(handler2.Events).Should(Receive())
			Expect(err).Should(HaveOccurred())
		})
	})
})
