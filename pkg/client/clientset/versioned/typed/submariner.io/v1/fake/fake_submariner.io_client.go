/*
Copyright The Kubernetes Authors.

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

// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	v1 "github.com/submariner-io/submariner/pkg/client/clientset/versioned/typed/submariner.io/v1"
	rest "k8s.io/client-go/rest"
	testing "k8s.io/client-go/testing"
)

type FakeSubmarinerV1 struct {
	*testing.Fake
}

func (c *FakeSubmarinerV1) Clusters(namespace string) v1.ClusterInterface {
	return &FakeClusters{c, namespace}
}

func (c *FakeSubmarinerV1) ClusterGlobalEgressIPs(namespace string) v1.ClusterGlobalEgressIPInterface {
	return &FakeClusterGlobalEgressIPs{c, namespace}
}

func (c *FakeSubmarinerV1) Endpoints(namespace string) v1.EndpointInterface {
	return &FakeEndpoints{c, namespace}
}

func (c *FakeSubmarinerV1) Gateways(namespace string) v1.GatewayInterface {
	return &FakeGateways{c, namespace}
}

func (c *FakeSubmarinerV1) GlobalEgressIPs(namespace string) v1.GlobalEgressIPInterface {
	return &FakeGlobalEgressIPs{c, namespace}
}

func (c *FakeSubmarinerV1) GlobalIngressIPs(namespace string) v1.GlobalIngressIPInterface {
	return &FakeGlobalIngressIPs{c, namespace}
}

// RESTClient returns a RESTClient that is used to communicate
// with API server by this client implementation.
func (c *FakeSubmarinerV1) RESTClient() rest.Interface {
	var ret *rest.RESTClient
	return ret
}
