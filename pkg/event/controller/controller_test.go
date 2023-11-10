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
	"context"
	"os"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/event/controller"
	"github.com/submariner-io/submariner/pkg/event/testing"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
)

const (
	testNamespace      = "test-namespace"
	testHandlerName    = "test-handler"
	testLocalClusterID = "local-cluster"
)

var _ = Describe("Event controller", func() {
	t := newTestDriver()

	When("a Node is created, updated and deleted", func() {
		It("should correctly notify the handler", func() {
			node := &corev1.Node{
				ObjectMeta: v1.ObjectMeta{
					Name: t.hostname,
				},
			}

			obj := test.CreateResource(t.nodes, node)
			node.Namespace = obj.GetNamespace()

			t.awaitEvent(testing.EvNodeCreated, node)

			node.Labels = map[string]string{"labeled-i-am": "i-am"}
			test.UpdateResource(t.nodes, node)

			t.awaitEvent(testing.EvNodeUpdated, node)

			Expect(t.nodes.Delete(context.TODO(), node.GetName(), v1.DeleteOptions{})).To(Succeed())

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
	endpoints       dynamic.ResourceInterface
	nodes           dynamic.ResourceInterface
	hostname        string
	testEvents      chan testing.TestEvent
	stopCh          chan struct{}
	eventController *controller.Controller
	handler         *TestHandler
}

func newTestDriver() *testDriver {
	t := &testDriver{}
	t.hostname, _ = os.Hostname()

	BeforeEach(func() {
		t.stopCh = make(chan struct{})

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
		_ = submV1.AddToScheme(scheme.Scheme)

		registry := event.NewRegistry("test-registry", "test-plugin")
		Expect(registry.AddHandlers(t.handler)).To(Succeed())

		config := controller.Config{
			RestMapper: test.GetRESTMapperFor(&corev1.Node{}, &submV1.Endpoint{}),
			Client:     dynamicfake.NewSimpleDynamicClient(scheme.Scheme),
			Registry:   registry,
		}

		os.Setenv("SUBMARINER_NAMESPACE", testNamespace)
		os.Setenv("SUBMARINER_CLUSTERID", testLocalClusterID)

		t.nodes = config.Client.Resource(*test.GetGroupVersionResourceFor(config.RestMapper, &corev1.Node{}))
		t.endpoints = config.Client.Resource(*test.GetGroupVersionResourceFor(config.RestMapper,
			&submV1.Endpoint{})).Namespace(testNamespace)

		var err error

		t.eventController, err = controller.New(&config)

		Expect(err).To(Succeed())
		Expect(t.eventController.Start(t.stopCh)).To(Succeed())
	})

	AfterEach(func() {
		close(t.stopCh)
		t.eventController.Stop()
	})

	return t
}

func (t *testDriver) createEndpoint(clusterID string) *submV1.Endpoint {
	endpoint := &submV1.Endpoint{
		ObjectMeta: v1.ObjectMeta{
			Name: string(uuid.NewUUID()),
		},
		Spec: submV1.EndpointSpec{
			ClusterID: clusterID,
			Hostname:  t.hostname,
		},
	}

	Expect(scheme.Scheme.Convert(test.CreateResource(t.endpoints, endpoint), endpoint, nil)).To(Succeed())

	return endpoint
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

	endpoint := t.createEndpoint(testLocalClusterID)

	t.awaitEvent(testing.EvLocalEndpointCreated, endpoint)
	t.awaitEvent(testing.EvTransitionToGateway, nil)

	t.createEndpoint(testLocalClusterID)
	Consistently(t.testEvents).ShouldNot(Receive(Equal(
		testing.TestEvent{Handler: testHandlerName, Name: testing.EvTransitionToGateway})))

	By("Update local Endpoint")

	endpoint.Labels = map[string]string{"labeled-i-am": "i-am"}
	test.UpdateResource(t.endpoints, endpoint)

	t.awaitEvent(testing.EvLocalEndpointUpdated, endpoint)

	By("Delete local Endpoint")

	Expect(t.endpoints.Delete(context.TODO(), endpoint.GetName(), v1.DeleteOptions{})).To(Succeed())

	t.awaitEvent(testing.EvLocalEndpointRemoved, endpoint)
	t.awaitEvent(testing.EvTransitionToNonGateway, nil)
	t.ensureNoEvents()
}

func (t *testDriver) testRemoteEndpoints() {
	By("Create first remote Endpoint")

	endpoint1 := t.createEndpoint("remote-cluster1")

	t.awaitEvent(testing.EvRemoteEndpointCreated, endpoint1)
	Expect(t.handler.remoteEndpoints.Load()).To(Equal([]submV1.Endpoint{*endpoint1}))

	By("Create second remote Endpoint")

	endpoint2 := t.createEndpoint("remote-cluster2")

	t.awaitEvent(testing.EvRemoteEndpointCreated, endpoint2)
	Expect(t.handler.remoteEndpoints.Load()).To(ContainElements(*endpoint1, *endpoint2))

	By("Update first remote Endpoint")

	endpoint1.Labels = map[string]string{"labeled-i-am": "i-am"}
	test.UpdateResource(t.endpoints, endpoint1)

	t.awaitEvent(testing.EvRemoteEndpointUpdated, endpoint1)

	By("Delete second remote Endpoint")

	Expect(t.endpoints.Delete(context.TODO(), endpoint2.GetName(), v1.DeleteOptions{})).To(Succeed())

	t.awaitEvent(testing.EvRemoteEndpointRemoved, endpoint2)
	Expect(t.handler.remoteEndpoints.Load()).To(Equal([]submV1.Endpoint{*endpoint1}))

	By("Delete first remote Endpoint")

	Expect(t.endpoints.Delete(context.TODO(), endpoint1.GetName(), v1.DeleteOptions{})).To(Succeed())

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
