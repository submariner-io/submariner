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

package testing

import (
	"context"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	submV1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/event/controller"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
)

const (
	Namespace      = "test-namespace"
	LocalClusterID = "local-cluster"
)

func init() {
	utilruntime.Must(submV1.AddToScheme(scheme.Scheme))
}

type ControllerSupport struct {
	Hostname  string
	endpoints dynamic.ResourceInterface
	nodes     dynamic.ResourceInterface
}

func NewControllerSupport() *ControllerSupport {
	c := &ControllerSupport{}
	c.Hostname, _ = os.Hostname()

	return c
}

func (c *ControllerSupport) Start(handler event.Handler) {
	stopCh := make(chan struct{})

	registry, err := event.NewRegistry("test-registry", handler.GetNetworkPlugins()[0], handler)
	Expect(err).To(Succeed())

	config := controller.Config{
		RestMapper: test.GetRESTMapperFor(&corev1.Node{}, &submV1.Endpoint{}),
		Client:     dynamicfake.NewSimpleDynamicClient(scheme.Scheme),
		Registry:   registry,
	}

	os.Setenv("SUBMARINER_NAMESPACE", Namespace)
	os.Setenv("SUBMARINER_CLUSTERID", LocalClusterID)

	c.nodes = config.Client.Resource(*test.GetGroupVersionResourceFor(config.RestMapper, &corev1.Node{}))
	c.endpoints = config.Client.Resource(*test.GetGroupVersionResourceFor(config.RestMapper, &submV1.Endpoint{})).Namespace(Namespace)

	eventController, err := controller.New(&config)

	Expect(err).To(Succeed())
	Expect(eventController.Start(stopCh)).To(Succeed())

	DeferCleanup(func() {
		close(stopCh)
		eventController.Stop()
	})
}

func NewEndpoint(clusterID, hostname string, subnets ...string) *submV1.Endpoint {
	return &submV1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: string(uuid.NewUUID()),
		},
		Spec: submV1.EndpointSpec{
			ClusterID: clusterID,
			Hostname:  hostname,
			Subnets:   subnets,
		},
	}
}

func (c *ControllerSupport) CreateLocalHostEndpoint() *submV1.Endpoint {
	return c.CreateEndpoint(NewEndpoint(LocalClusterID, c.Hostname))
}

func (c *ControllerSupport) CreateEndpoint(endpoint *submV1.Endpoint) *submV1.Endpoint {
	Expect(scheme.Scheme.Convert(test.CreateResource(c.endpoints, endpoint), endpoint, nil)).To(Succeed())
	return endpoint
}

func (c *ControllerSupport) UpdateEndpoint(endpoint *submV1.Endpoint) {
	test.UpdateResource(c.endpoints, endpoint)
}

func (c *ControllerSupport) DeleteEndpoint(name string) {
	Expect(c.endpoints.Delete(context.TODO(), name, metav1.DeleteOptions{})).To(Succeed())
}

func NewNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func (c *ControllerSupport) CreateNode(node *corev1.Node) *corev1.Node {
	Expect(scheme.Scheme.Convert(test.CreateResource(c.nodes, node), node, nil)).To(Succeed())
	return node
}

func (c *ControllerSupport) UpdateNode(node *corev1.Node) {
	test.UpdateResource(c.nodes, node)
}

func (c *ControllerSupport) DeleteNode(name string) {
	Expect(c.nodes.Delete(context.TODO(), name, metav1.DeleteOptions{})).To(Succeed())
}
