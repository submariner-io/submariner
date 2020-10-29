package event_test

import (
	"testing"

	k8sV1 "k8s.io/api/core/v1"

	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestEventPackage(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Event suite")
}

type TestEvent struct {
	Name      string
	Handler   string
	Parameter interface{}
}

type TestHandler struct {
	event.HandlerBase
	Name          string
	NetworkPlugin string
	FailOnEvent   error
	Events        chan TestEvent
	Initialized   bool
}

var allTestEvents chan TestEvent = make(chan TestEvent, 1000)

func cleanupTestEvents() {
	allTestEvents = make(chan TestEvent, 1000)
}

func NewTestHandler(name, networkPlugin string) *TestHandler {
	return &TestHandler{
		Name:          name,
		NetworkPlugin: networkPlugin,
		Events:        make(chan TestEvent, 100),
		Initialized:   false,
	}
}

func (t *TestHandler) addEvent(eventName string, param interface{}) error {
	if t.FailOnEvent == nil {
		ev := TestEvent{
			Name:      eventName,
			Parameter: param,
		}

		t.Events <- ev

		// On the global channel we also include the handler name
		// so we can verify ordering
		ev.Handler = t.Name
		allTestEvents <- ev
	}

	return t.FailOnEvent
}
func (t *TestHandler) Init() error {
	t.Initialized = true

	return nil
}

func (t *TestHandler) GetName() string {
	return t.Name
}

func (t *TestHandler) GetNetworkPlugin() string {
	return t.NetworkPlugin
}

const evTransitionToNonGateway = "TransitionToNonGateway"
const evTransitionToGateway = "TransitionToGateway"
const evLocalEndpointCreated = "LocalEndpointCreated"
const evLocalEndpointUpdated = "LocalEndpointUpdated"
const evLocalEndpointRemoved = "LocalEndpointRemoved"
const evRemoteEndpointCreated = "RemoteEndpointCreated"
const evRemoteEndpointUpdated = "RemoteEndpointUpdated"
const evRemoteEndpointRemoved = "RemoteEndpointRemoved"
const evNodeCreated = "NodeCreated"
const evNodeUpdated = "NodeUpdated"
const evNodeRemoved = "NodeRemoved"
const evStop = "Stop"

func (t *TestHandler) Stop(uninstall bool) error {
	return t.addEvent(evStop, uninstall)
}

func (t *TestHandler) TransitionToNonGateway() error {
	return t.addEvent(evTransitionToNonGateway, nil)
}

func (t *TestHandler) TransitionToGateway() error {
	return t.addEvent(evTransitionToGateway, nil)
}

func (t *TestHandler) LocalEndpointCreated(endpoint *submV1.Endpoint) error {
	return t.addEvent(evLocalEndpointCreated, endpoint)
}

func (t *TestHandler) LocalEndpointUpdated(endpoint *submV1.Endpoint) error {
	return t.addEvent(evLocalEndpointUpdated, endpoint)
}

func (t *TestHandler) LocalEndpointRemoved(endpoint *submV1.Endpoint) error {
	return t.addEvent(evLocalEndpointRemoved, endpoint)
}

func (t *TestHandler) RemoteEndpointCreated(endpoint *submV1.Endpoint) error {
	return t.addEvent(evRemoteEndpointCreated, endpoint)
}

func (t *TestHandler) RemoteEndpointUpdated(endpoint *submV1.Endpoint) error {
	return t.addEvent(evRemoteEndpointUpdated, endpoint)
}

func (t *TestHandler) RemoteEndpointRemoved(endpoint *submV1.Endpoint) error {
	return t.addEvent(evRemoteEndpointRemoved, endpoint)
}

func (t *TestHandler) NodeCreated(node *k8sV1.Node) error {
	return t.addEvent(evNodeCreated, node)
}

func (t *TestHandler) NodeUpdated(node *k8sV1.Node) error {
	return t.addEvent(evNodeUpdated, node)
}

func (t *TestHandler) NodeRemoved(node *k8sV1.Node) error {
	return t.addEvent(evNodeRemoved, node)
}
