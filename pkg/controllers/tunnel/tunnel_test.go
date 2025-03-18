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

package tunnel_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log/kzerolog"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/admiral/pkg/watcher"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cable"
	"github.com/submariner-io/submariner/pkg/cable/fake"
	"github.com/submariner-io/submariner/pkg/cableengine"
	"github.com/submariner-io/submariner/pkg/controllers/tunnel"
	submendpoint "github.com/submariner-io/submariner/pkg/endpoint"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	"github.com/submariner-io/submariner/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	fakeClient "k8s.io/client-go/dynamic/fake"
	kubeScheme "k8s.io/client-go/kubernetes/scheme"
	k8snet "k8s.io/utils/net"
)

const (
	namespace = "submariner"
	ipV4CIDR  = "1.2.3.4/16"
	ipV6CIDR  = "2002::1234:abcd:ffff:c0a8:101/64"
)

func init() {
	kzerolog.AddFlags(nil)
}

var fakeDriver *fake.Driver

var _ = BeforeSuite(func() {
	kzerolog.InitK8sLogging()
	cable.AddDriver(fake.DriverName, func(_ *submendpoint.Local, _ *types.SubmarinerCluster) (cable.Driver, error) {
		return fakeDriver, nil
	})
})

var _ = Describe("Managing tunnels", func() {
	var (
		config      *watcher.Config
		endpoints   dynamic.ResourceInterface
		endpoint    *v1.Endpoint
		localEPSpec *v1.EndpointSpec
		stopCh      chan struct{}
	)

	BeforeEach(func() {
		fakeDriver = fake.New()

		localEPSpec = &v1.EndpointSpec{
			Backend: fake.DriverName,
			Subnets: []string{ipV4CIDR},
		}

		endpoint = &v1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "east-submariner-cable-east-192-68-1-1",
				Namespace: namespace,
			},
			Spec: v1.EndpointSpec{
				CableName:  "submariner-cable-east-192-68-1-1",
				ClusterID:  "east",
				Hostname:   "redsox",
				PrivateIPs: []string{"192.68.1.2"},
				Subnets:    []string{ipV4CIDR},
			},
		}

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
		localEp := submendpoint.NewLocal(localEPSpec, fakeClient.NewSimpleDynamicClient(kubeScheme.Scheme), "")

		engine := cableengine.NewEngine(&types.SubmarinerCluster{}, localEp)

		nat, err := natdiscovery.New(localEp)
		Expect(err).To(Succeed())

		engine.SetupNATDiscovery(nat)

		Expect(engine.StartEngine()).To(Succeed())

		stopCh = make(chan struct{})

		Expect(tunnel.StartController(engine, namespace, config, stopCh)).To(Succeed())

		test.CreateResource(endpoints, endpoint)
	})

	AfterEach(func() {
		close(stopCh)
	})

	verifyConnectToEndpoint := func(family k8snet.IPFamily) {
		fakeDriver.AwaitConnectToEndpoint(&natdiscovery.NATEndpointInfo{
			UseIP:     endpoint.Spec.GetPrivateIP(family),
			UseNAT:    false,
			Endpoint:  *endpoint,
			UseFamily: family,
		})
	}

	verifyDisconnectFromEndpoint := func(family k8snet.IPFamily) {
		fakeDriver.AwaitDisconnectFromEndpoint(&endpoint.Spec, family)
	}

	When("an Endpoint is created/deleted", func() {
		testAddRemoveCable := func(family k8snet.IPFamily) {
			It(fmt.Sprintf("should install/remove the IPv%s cable", family), func() {
				verifyConnectToEndpoint(family)
				fakeDriver.AwaitNoConnectToEndpoint()

				Expect(endpoints.Delete(context.TODO(), endpoint.Name, metav1.DeleteOptions{})).To(Succeed())
				verifyDisconnectFromEndpoint(family)
			})
		}

		Context("that supports only IPv4", func() {
			Context("and the local Endpoint supports only IPv4", func() {
				testAddRemoveCable(k8snet.IPv4)
			})

			Context("and the local Endpoint supports dual-stack", func() {
				BeforeEach(func() {
					localEPSpec.Subnets = []string{ipV6CIDR, ipV4CIDR}
				})

				testAddRemoveCable(k8snet.IPv4)
			})

			Context("and the local Endpoint supports only IPv6", func() {
				BeforeEach(func() {
					localEPSpec.Subnets = []string{ipV6CIDR}
				})

				It("should not install the cable", func() {
					fakeDriver.AwaitNoConnectToEndpoint()
					fakeDriver.AwaitNoDisconnectFromEndpoint()
				})
			})
		})

		Context("that supports only IPv6", func() {
			BeforeEach(func() {
				endpoint.Spec.Subnets = []string{ipV6CIDR}
			})

			Context("and the local Endpoint supports only IPv6", func() {
				BeforeEach(func() {
					localEPSpec.Subnets = []string{ipV6CIDR}
				})

				testAddRemoveCable(k8snet.IPv6)
			})

			Context("and the local Endpoint supports dual-stack", func() {
				BeforeEach(func() {
					localEPSpec.Subnets = []string{ipV6CIDR, ipV4CIDR}
				})

				testAddRemoveCable(k8snet.IPv6)
			})

			Context("and the local Endpoint supports only IPv4", func() {
				BeforeEach(func() {
					localEPSpec.Subnets = []string{ipV4CIDR}
				})

				It("should not install the cable", func() {
					fakeDriver.AwaitNoConnectToEndpoint()
					fakeDriver.AwaitNoDisconnectFromEndpoint()
				})
			})
		})

		Context("that supports dual-stack", func() {
			BeforeEach(func() {
				endpoint.Spec.Subnets = []string{ipV6CIDR, ipV4CIDR}
			})

			Context("and the local Endpoint supports only IPv6", func() {
				BeforeEach(func() {
					localEPSpec.Subnets = []string{ipV6CIDR}
				})

				testAddRemoveCable(k8snet.IPv6)
			})

			Context("and the local Endpoint supports dual-stack", func() {
				BeforeEach(func() {
					localEPSpec.Subnets = []string{ipV6CIDR, ipV4CIDR}
				})

				It("should install/remove both cables", func() {
					verifyConnectToEndpoint(k8snet.IPv6)
					verifyConnectToEndpoint(k8snet.IPv4)

					Expect(endpoints.Delete(context.TODO(), endpoint.Name, metav1.DeleteOptions{})).To(Succeed())
					verifyDisconnectFromEndpoint(k8snet.IPv6)
					verifyDisconnectFromEndpoint(k8snet.IPv4)
				})
			})

			Context("and the local Endpoint supports only IPv4", func() {
				BeforeEach(func() {
					localEPSpec.Subnets = []string{ipV4CIDR}
				})

				It("should not install the IPv4 cable", func() {
					verifyConnectToEndpoint(k8snet.IPv4)
					fakeDriver.AwaitNoDisconnectFromEndpoint()
				})
			})
		})
	})

	When("an Endpoint is updated", func() {
		It("should install the cable", func() {
			verifyConnectToEndpoint(k8snet.IPv4)

			endpoint.Spec.PrivateIPs = []string{"192.68.1.3"}
			test.UpdateResource(endpoints, endpoint)

			verifyConnectToEndpoint(k8snet.IPv4)
		})
	})

	When("install cable initially fails", func() {
		BeforeEach(func() {
			config.ResyncPeriod = time.Millisecond * 500
			fakeDriver.ErrOnConnectToEndpoint = errors.New("fake connect error")
		})

		It("should retry until it succeeds", func() {
			verifyConnectToEndpoint(k8snet.IPv4)
		})
	})

	When("remove cable initially fails", func() {
		BeforeEach(func() {
			fakeDriver.ErrOnDisconnectFromEndpoint = errors.New("fake disconnect error")
		})

		It("should retry until it succeeds", func() {
			verifyConnectToEndpoint(k8snet.IPv4)

			Expect(endpoints.Delete(context.TODO(), endpoint.Name, metav1.DeleteOptions{})).To(Succeed())
			verifyDisconnectFromEndpoint(k8snet.IPv4)
		})
	})
})

func TestTunnelController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tunnel controller Suite")
}
