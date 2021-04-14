/*
© 2021 Red Hat, Inc. and others

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
package tunnel_test

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/admiral/pkg/watcher"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cable/fake"
	"github.com/submariner-io/submariner/pkg/cableengine"
	"github.com/submariner-io/submariner/pkg/controllers/tunnel"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	"github.com/submariner-io/submariner/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	fakeClient "k8s.io/client-go/dynamic/fake"
	kubeScheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog"
)

const (
	namespace = "submariner"
)

func init() {
	klog.InitFlags(nil)
}

var fakeDriver *fake.Driver

var _ = BeforeSuite(func() {
	cable.AddDriver(fake.DriverName, func(endpoint types.SubmarinerEndpoint, cluster types.SubmarinerCluster) (cable.Driver, error) {
		return fakeDriver, nil
	})
})

var _ = Describe("Managing tunnels", func() {
	var (
		config    *watcher.Config
		endpoints dynamic.ResourceInterface
		endpoint  *v1.Endpoint
		stopCh    chan struct{}
	)

	BeforeEach(func() {
		fakeDriver = fake.New()

		endpoint = &v1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "east-submariner-cable-east-192-68-1-1",
				Namespace: namespace,
			},
			Spec: v1.EndpointSpec{
				CableName: "submariner-cable-east-192-68-1-1",
				ClusterID: "east",
				Hostname:  "redsox",
				PrivateIP: "192.68.1.2",
			}}

		Expect(v1.AddToScheme(kubeScheme.Scheme)).To(Succeed())

		scheme := runtime.NewScheme()
		Expect(v1.AddToScheme(scheme)).To(Succeed())

		client := fakeClient.NewSimpleDynamicClient(scheme)

		restMapper := test.GetRESTMapperFor(&v1.Endpoint{}, &v1.Cluster{})
		gvr := test.GetGroupVersionResourceFor(restMapper, &v1.Endpoint{})

		endpoints = client.Resource(*gvr).Namespace(namespace)

		config = &watcher.Config{
			RestMapper: restMapper,
			Client:     client,
			Scheme:     scheme,
		}
	})

	JustBeforeEach(func() {
		engine := cableengine.NewEngine(types.SubmarinerCluster{}, types.SubmarinerEndpoint{
			Spec: v1.EndpointSpec{
				Backend: fake.DriverName,
			},
		})

		nat, err := natdiscovery.New(&types.SubmarinerEndpoint{})
		Expect(err).To(Succeed())

		engine.SetupNATDiscovery(nat)

		Expect(engine.StartEngine()).To(Succeed())

		stopCh = make(chan struct{})

		Expect(tunnel.StartController(engine, namespace, config, stopCh)).To(Succeed())
	})

	AfterEach(func() {
		close(stopCh)
	})

	verifyConnectToEndpoint := func() {
		fakeDriver.AwaitConnectToEndpoint(&natdiscovery.NATEndpointInfo{
			UseIP:    endpoint.Spec.PrivateIP,
			UseNAT:   false,
			Endpoint: types.SubmarinerEndpoint{Spec: endpoint.Spec},
		})
	}

	verifyDisconnectFromEndpoint := func() {
		fakeDriver.AwaitDisconnectFromEndpoint(&types.SubmarinerEndpoint{Spec: endpoint.Spec})
	}

	When("an Endpoint is created", func() {
		It("should install the cable", func() {
			test.CreateResource(endpoints, endpoint)
			verifyConnectToEndpoint()
		})
	})

	When("an Endpoint is updated", func() {
		It("should install the cable", func() {
			test.CreateResource(endpoints, endpoint)
			verifyConnectToEndpoint()

			endpoint.Spec.Subnets = []string{"100.0.0.0/16", "10.0.0.0/14"}
			test.UpdateResource(endpoints, endpoint)

			verifyConnectToEndpoint()
		})
	})

	When("an Endpoint is deleted", func() {
		It("should remove the cable", func() {
			test.CreateResource(endpoints, endpoint)
			verifyConnectToEndpoint()

			Expect(endpoints.Delete(endpoint.Name, nil)).To(Succeed())
			verifyDisconnectFromEndpoint()
		})
	})

	When("install cable initially fails", func() {
		BeforeEach(func() {
			config.ResyncPeriod = time.Millisecond * 500
			fakeDriver.ErrOnConnectToEndpoint = errors.New("fake connect error")
		})

		It("should retry until it succeeds", func() {
			test.CreateResource(endpoints, endpoint)
			verifyConnectToEndpoint()
		})
	})

	When("remove cable initially fails", func() {
		BeforeEach(func() {
			fakeDriver.ErrOnDisconnectFromEndpoint = errors.New("fake disconnect error")
		})

		It("should retry until it succeeds", func() {
			test.CreateResource(endpoints, endpoint)
			verifyConnectToEndpoint()

			Expect(endpoints.Delete(endpoint.Name, nil)).To(Succeed())
			verifyDisconnectFromEndpoint()
		})
	})
})

func TestTunnelController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tunnel controller Suite")
}
