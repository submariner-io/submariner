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
	"fmt"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/event/controller"
	"github.com/submariner-io/submariner/pkg/event/testing"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
)

const (
	testNamespace      = "test-namespace"
	testHandlerName    = "test-handler"
	testLocalClusterID = "local-cluster"
)

var _ = Describe("Event controller", func() {
	var (
		endpoints       dynamic.ResourceInterface
		nodes           dynamic.ResourceInterface
		node            *corev1.Node
		endpoint        *submV1.Endpoint
		hostname        string
		testEvents      chan testing.TestEvent
		stopCh          chan struct{}
		registry        *event.Registry
		eventController *controller.Controller
	)

	BeforeEach(func() {
		stopCh = make(chan struct{})
		testEvents = make(chan testing.TestEvent, 1000)
		testHandler := testing.NewTestHandler(testHandlerName, event.AnyNetworkPlugin, testEvents)
		registry = event.NewRegistry("test-registry", "test-plugin")
		Expect(registry.AddHandlers(testHandler)).To(Succeed())
		hostname, _ = os.Hostname()
		node = NewNode(hostname)
	})

	JustBeforeEach(func() {
		_ = submV1.AddToScheme(scheme.Scheme)

		config := controller.Config{
			RestMapper: test.GetRESTMapperFor(&corev1.Node{}, &submV1.Endpoint{}),
			Client:     fake.NewDynamicClient(scheme.Scheme),
			Registry:   registry,
		}
		os.Setenv("SUBMARINER_NAMESPACE", testNamespace)
		os.Setenv("SUBMARINER_CLUSTERID", testLocalClusterID)

		nodes = config.Client.Resource(*test.GetGroupVersionResourceFor(config.RestMapper, &corev1.Node{}))
		endpoints = config.Client.Resource(*test.GetGroupVersionResourceFor(config.RestMapper,
			&submV1.Endpoint{})).Namespace(testNamespace)

		var err error

		eventController, err = controller.New(&config)

		Expect(err).To(Succeed())
		Expect(eventController.Start(stopCh)).To(Succeed())
	})

	AfterEach(func() {
		close(stopCh)
		eventController.Stop()
	})

	When("a Node is created, updated and deleted", func() {
		It("should notify the appropriate handler of each event", func() {
			obj := test.CreateResource(nodes, node)
			node.Namespace = obj.GetNamespace()
			node.ResourceVersion = obj.GetResourceVersion()
			node.UID = obj.GetUID()

			Eventually(testEvents).Should(Receive(Equal(
				testing.TestEvent{Handler: testHandlerName, Name: testing.EvNodeCreated, Parameter: node})))
			Consistently(testEvents).ShouldNot(Receive())

			node.Labels = map[string]string{"labeled-i-am": "i-am"}

			test.UpdateResource(nodes, node)

			Eventually(testEvents).Should(Receive(Equal(
				testing.TestEvent{Handler: testHandlerName, Name: testing.EvNodeUpdated, Parameter: node})))
			Consistently(testEvents).ShouldNot(Receive())

			Expect(nodes.Delete(context.TODO(), node.GetName(), v1.DeleteOptions{})).To(Succeed())

			Eventually(testEvents).Should(Receive(Equal(
				testing.TestEvent{Handler: testHandlerName, Name: testing.EvNodeRemoved, Parameter: node})))
			Consistently(testEvents).ShouldNot(Receive())
		})
	})

	When("a Local Endpoint is created on this host, updated and deleted", func() {
		It("should notify the appropriate handlers of each event", func() {
			endpoint = NewEndpoint(testLocalClusterID, hostname)
			obj := test.CreateResource(endpoints, endpoint)
			endpoint.Namespace = obj.GetNamespace()
			endpoint.ResourceVersion = obj.GetResourceVersion()
			endpoint.UID = obj.GetUID()

			Eventually(testEvents).Should(Receive(Equal(
				testing.TestEvent{Handler: testHandlerName, Name: testing.EvLocalEndpointCreated, Parameter: endpoint})))
			Eventually(testEvents).Should(Receive(Equal(
				testing.TestEvent{Handler: testHandlerName, Name: testing.EvTransitionToGateway})))
			Consistently(testEvents).ShouldNot(Receive())

			endpoint.Labels = map[string]string{"labeled-i-am": "i-am"}

			test.UpdateResource(endpoints, endpoint)

			Eventually(testEvents).Should(Receive(Equal(
				testing.TestEvent{Handler: testHandlerName, Name: testing.EvLocalEndpointUpdated, Parameter: endpoint})))
			Consistently(testEvents).ShouldNot(Receive())

			Expect(endpoints.Delete(context.TODO(), endpoint.GetName(), v1.DeleteOptions{})).To(Succeed())

			Eventually(testEvents).Should(Receive(Equal(
				testing.TestEvent{Handler: testHandlerName, Name: testing.EvLocalEndpointRemoved, Parameter: endpoint})))
			Eventually(testEvents).Should(Receive(Equal(
				testing.TestEvent{Handler: testHandlerName, Name: testing.EvTransitionToNonGateway})))
			Consistently(testEvents).ShouldNot(Receive())
		})
	})
})

func NewNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: v1.ObjectMeta{
			Name:            name,
			UID:             uuid.NewUUID(),
			ResourceVersion: "10",
		},
		Spec: corev1.NodeSpec{},
	}
}

func NewEndpoint(clusterID, hostname string) *submV1.Endpoint {
	return &submV1.Endpoint{
		ObjectMeta: v1.ObjectMeta{
			Name:            fmt.Sprintf("%s-%s", clusterID, hostname),
			UID:             uuid.NewUUID(),
			ResourceVersion: "10",
		},
		Spec: submV1.EndpointSpec{
			ClusterID: clusterID,
			Hostname:  hostname,
		},
	}
}
