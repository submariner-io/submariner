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
	When("handlers with various network plugins are added to the registry", func() {
		var (
			matchingHandlers    []*TestHandler
			nonMatchingHandlers []*TestHandler
			registry            event.Registry
			allTestEvents       chan TestEvent
		)

		BeforeEach(func() {
			allTestEvents = make(chan TestEvent, 1000)
			registry = event.NewRegistry("test-registry", npGenericKubeproxyIptables)

			nonMatchingHandlers = []*TestHandler{
				NewTestHandler("ovn-handler", npOVNKubernetes, allTestEvents),
			}

			matchingHandlers = []*TestHandler{
				NewTestHandler("kubeproxy-handler1", npGenericKubeproxyIptables, allTestEvents),
				NewTestHandler("kubeproxy-handler2", npGenericKubeproxyIptables, allTestEvents),
				NewTestHandler("wildcard-handler", event.AnyNetworkPlugin, allTestEvents),
			}

			err := registry.AddHandlers(logger.NewHandler(), matchingHandlers[0], nonMatchingHandlers[0], matchingHandlers[1],
				matchingHandlers[2])
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

		It("should invoke all matching handlers of all events", func() {
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

				for _, h := range matchingHandlers {
					ev.Handler = h.Name
					Expect(allTestEvents).To(Receive(Equal(ev)))
				}
			}

			Expect(allTestEvents).ToNot(Receive())
		})

		When("one handler returns an error", func() {
			It("should invoke subsequent matching handlers", func() {
				matchingHandlers[0].FailOnEvent = errors.New("I shall fail!")
				err := registry.TransitionToGateway()

				for _, h := range matchingHandlers {
					Expect(h.Events).To(Receive())
				}

				Expect(err).To(HaveOccurred())
			})
		})
	})
})
