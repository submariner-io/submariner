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

package healthchecker_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	fakeClient "github.com/submariner-io/submariner/pkg/client/clientset/versioned/fake"
	submarinerv1client "github.com/submariner-io/submariner/pkg/client/clientset/versioned/typed/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	eventtesting "github.com/submariner-io/submariner/pkg/event/testing"
	"github.com/submariner-io/submariner/pkg/pinger"
	"github.com/submariner-io/submariner/pkg/pinger/fake"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/handlers/healthchecker"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubeScheme "k8s.io/client-go/kubernetes/scheme"
	k8snet "k8s.io/utils/net"
)

const (
	namespace       = "submariner"
	remoteClusterID = "west"
	healthCheckIP1  = "1.1.1.1"
	healthCheckIP2  = "2.2.2.2"
	localNodeName   = "nodeName"
)

var _ = Describe("RouteAgent syncing", func() {
	t := newTestDriver()

	It("should create a RouteAgent resource", func() {
		t.awaitRouteAgent(nil)
	})

	When("a remote Endpoint is created/updated/deleted", func() {
		It("should add/update/delete its RemoteEndpoint information in the RouteAgent resource", func() {
			endpoint := t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))

			t.awaitRemoteEndpoint(func(ep *submarinerv1.RemoteEndpoint, g Gomega) {
				g.Expect(ep.Spec).To(Equal(endpoint.Spec))
			})

			By("Updating remote endpoint")

			endpoint.Spec.Hostname = "newHostName"
			t.UpdateEndpoint(endpoint)

			t.awaitRemoteEndpoint(func(ep *submarinerv1.RemoteEndpoint, g Gomega) {
				g.Expect(ep.Spec.Hostname).To(Equal(endpoint.Spec.Hostname))
				g.Expect(ep.Spec).To(Equal(endpoint.Spec))
			})

			By("Deleting remote endpoint")

			t.DeleteEndpoint(endpoint.Name)

			t.awaitRouteAgent(func(ra *submarinerv1.RouteAgent, g Gomega) {
				g.Expect(ra.Status.RemoteEndpoints).To(BeEmpty())
			})
		})
	})

	When("a stale remote Endpoint is deleted", func() {
		It("should remove its RemoteEndpoint information in the RouteAgent resource", func() {
			endpoint1 := t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))

			t.awaitRemoteEndpoint(nil)

			By("Creating new remote endpoint")

			endpoint2 := t.newSubmEndpoint(healthCheckIP2)
			endpoint2.Spec.CableName = "new-cable"
			endpoint2.Name = "new-endpoint"
			endpoint2.CreationTimestamp = metav1.Time{Time: metav1.Now().Add(time.Second)}
			t.CreateEndpoint(endpoint2)

			t.awaitRouteAgent(func(ra *submarinerv1.RouteAgent, g Gomega) {
				g.Expect(ra.Status.RemoteEndpoints).To(HaveLen(2))
			})

			By("Deleting stale remote endpoint")

			t.DeleteEndpoint(endpoint1.Name)

			t.awaitRouteAgent(func(ra *submarinerv1.RouteAgent, g Gomega) {
				g.Expect(ra.Status.RemoteEndpoints).To(HaveLen(1))
				g.Expect(ra.Status.RemoteEndpoints[0].Spec.GetHealthCheckIP(k8snet.IPv4)).To(Equal(healthCheckIP2))
			})
		})
	})
})

var _ = Describe("RemoteEndpoint latency info", func() {
	t := newTestDriver()

	When("a remote Endpoint is created", func() {
		It("should start a pinger and correctly update the RemoteEndpoint Status and LatencyInfo", func() {
			t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))
			t.pingerMap[healthCheckIP1].AwaitStart()

			latencyInfo := t.newLatencyInfo(k8snet.IPv4)
			t.setLatencyInfo(healthCheckIP1, latencyInfo)

			t.awaitRemoteEndpoint(func(ep *submarinerv1.RemoteEndpoint, g Gomega) {
				g.Expect(ep.Status).To(Equal(submarinerv1.Connected))
				g.Expect(ep.LatencyRTT).To(Equal(latencyInfo.Spec))
			})
		})

		Context("with no HealthCheckIP", func() {
			It("should not start a pinger and should set the RemoteEndpoint Status to None", func() {
				endpoint := t.newSubmEndpoint()
				endpoint.Spec.Subnets = []string{"2.2.2.2/24"}
				t.CreateEndpoint(endpoint)
				t.pingerMap[healthCheckIP1].AwaitNoStart()

				t.awaitRemoteEndpoint(func(ep *submarinerv1.RemoteEndpoint, g Gomega) {
					g.Expect(ep.Status).To(Equal(submarinerv1.ConnectionNone))
					g.Expect(ep.Spec).To(Equal(endpoint.Spec))
				})
			})
		})

		Context("on the gateway", func() {
			It("should not start a pinger and should set the RemoteEndpoint Status to None", func() {
				_ = t.CreateLocalHostEndpoint()
				endpoint1 := t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))
				t.pingerMap[healthCheckIP1].AwaitNoStart()

				t.awaitRemoteEndpoint(func(ep *submarinerv1.RemoteEndpoint, g Gomega) {
					g.Expect(ep.Status).To(Equal(submarinerv1.ConnectionNone))
					g.Expect(ep.Spec).To(Equal(endpoint1.Spec))
				})
			})
		})

		Context("with health check not enabled", func() {
			BeforeEach(func() {
				t.healthcheckerEnabled = false
			})

			It("should not start a pinger and should set the RemoteEndpoint Status to None", func() {
				endpoint1 := t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))
				t.pingerMap[healthCheckIP1].AwaitNoStart()

				t.awaitRemoteEndpoint(func(ep *submarinerv1.RemoteEndpoint, g Gomega) {
					g.Expect(ep.Status).To(Equal(submarinerv1.ConnectionNone))
					g.Expect(ep.Spec).To(Equal(endpoint1.Spec))
				})
			})
		})
	})

	When("a remote Endpoint is updated and the HealthCheckIP was changed", func() {
		It("should stop the pinger and start a new one", func() {
			endpoint1 := t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))

			t.pingerMap[healthCheckIP1].AwaitStart()

			endpoint1.Spec.HealthCheckIPs = []string{healthCheckIP2}

			t.UpdateEndpoint(endpoint1)
			t.pingerMap[healthCheckIP1].AwaitStop()
			t.pingerMap[healthCheckIP2].AwaitStart()
		})
	})

	When("a remote Endpoint is deleted", func() {
		It("should stop the pinger", func() {
			endpoint1 := t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))
			t.pingerMap[healthCheckIP1].AwaitStart()

			t.DeleteEndpoint(endpoint1.Name)
			t.pingerMap[healthCheckIP1].AwaitStop()
		})
	})

	When("a remote Endpoint with dual-stack health check IPs is created/deleted", func() {
		const healthCheckIPv6 = "2001:db8:3333:4444:5555:6666:7777:8888"

		BeforeEach(func() {
			t.supportedIPFamilies = []k8snet.IPFamily{k8snet.IPv4, k8snet.IPv6}
			t.pingerMap[healthCheckIPv6] = fake.NewPinger(healthCheckIPv6)
		})

		It("should start/stop Pingers and return the correct LatencyInfo for both", func() {
			endpoint := t.newSubmEndpoint(healthCheckIP1, healthCheckIPv6)
			endpoint.Spec.PublicIPs = []string{"2002:0:0:1234::", "2.2.2.2"}
			endpoint.Spec.PrivateIPs = []string{"2003:0:0:1234::", "3.3.3.3"}

			t.CreateEndpoint(endpoint)
			t.pingerMap[healthCheckIP1].AwaitStart()
			t.pingerMap[healthCheckIPv6].AwaitStart()

			ipv4LatencyInfo := t.newLatencyInfo(k8snet.IPv4)
			t.setLatencyInfo(healthCheckIP1, ipv4LatencyInfo)

			ipv6LatencyInfo := t.newLatencyInfo(k8snet.IPv6)
			t.setLatencyInfo(healthCheckIPv6, ipv6LatencyInfo)

			t.awaitRouteAgent(func(ra *submarinerv1.RouteAgent, g Gomega) {
				epMap := map[string]*submarinerv1.RemoteEndpoint{}
				for i := range ra.Status.RemoteEndpoints {
					g.Expect(ra.Status.RemoteEndpoints[i].Spec.HealthCheckIPs).To(HaveLen(1))
					epMap[ra.Status.RemoteEndpoints[i].Spec.HealthCheckIPs[0]] = &ra.Status.RemoteEndpoints[i]
				}

				ipv4Endpoint := epMap[healthCheckIP1]
				g.Expect(ipv4Endpoint).ToNot(BeNil(), "RemoteEndpoint not found for IPv4 health check IP %q", healthCheckIP1)
				g.Expect(ipv4Endpoint.Status).To(Equal(submarinerv1.Connected))
				g.Expect(ipv4Endpoint.LatencyRTT).To(Equal(ipv4LatencyInfo.Spec))

				spec := endpoint.Spec
				spec.HealthCheckIPs = []string{healthCheckIP1}
				spec.PublicIPs = []string{endpoint.Spec.GetPublicIP(k8snet.IPv4)}
				spec.PrivateIPs = []string{endpoint.Spec.GetPrivateIP(k8snet.IPv4)}
				g.Expect(ipv4Endpoint.Spec).To(Equal(spec))

				ipv6Endpoint := epMap[healthCheckIPv6]
				g.Expect(ipv6Endpoint).ToNot(BeNil(), "RemoteEndpoint not found for IPv6 health check IP %q", healthCheckIP1)
				g.Expect(ipv6Endpoint.Status).To(Equal(submarinerv1.Connected))
				g.Expect(ipv6Endpoint.LatencyRTT).To(Equal(ipv6LatencyInfo.Spec))

				spec = endpoint.Spec
				spec.HealthCheckIPs = []string{healthCheckIPv6}
				spec.PublicIPs = []string{endpoint.Spec.GetPublicIP(k8snet.IPv6)}
				spec.PrivateIPs = []string{endpoint.Spec.GetPrivateIP(k8snet.IPv6)}
				g.Expect(ipv6Endpoint.Spec).To(Equal(spec))

				g.Expect(ra.Status.RemoteEndpoints).To(HaveLen(2))
			})

			By("Deleting Endpoint")

			t.DeleteEndpoint(endpoint.Name)

			t.pingerMap[healthCheckIP1].AwaitStop()
			t.pingerMap[healthCheckIPv6].AwaitStop()

			t.awaitRouteAgent(func(ra *submarinerv1.RouteAgent, g Gomega) {
				g.Expect(ra.Status.RemoteEndpoints).To(BeEmpty())
			})
		})
	})

	When("a pinger reports a connection error", func() {
		It(" should set the RemoteEndpoint Status to Error", func() {
			t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))

			latencyInfo := &pinger.LatencyInfo{
				ConnectionStatus: pinger.ConnectionError,
				ConnectionError:  "pinger failed",
			}

			t.setLatencyInfo(healthCheckIP1, latencyInfo)

			t.awaitRemoteEndpoint(func(ep *submarinerv1.RemoteEndpoint, g Gomega) {
				g.Expect(ep.Status).To(Equal(submarinerv1.ConnectionError))
				g.Expect(ep.StatusMessage).To(Equal(latencyInfo.ConnectionError))
			})
		})
	})
})

var _ = Describe("Gateway transition", func() {
	t := newTestDriver()

	Context("to gateway node", func() {
		It("should stop the pinger", func() {
			_ = t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))
			t.pingerMap[healthCheckIP1].AwaitStart()

			_ = t.CreateLocalHostEndpoint()
			t.pingerMap[healthCheckIP1].AwaitStop()
		})
	})

	Context("to non-gateway node", func() {
		It("should start the pinger", func() {
			endpoint := t.CreateLocalHostEndpoint()
			_ = t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))
			t.pingerMap[healthCheckIP1].AwaitNoStart()

			t.DeleteEndpoint(endpoint.Name)
			t.pingerMap[healthCheckIP1].AwaitStart()
		})
	})
})

var _ = Describe("Stop", func() {
	t := newTestDriver()

	It("should stop the Pingers and delete the RouteAgent resource", func() {
		t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))
		t.pingerMap[healthCheckIP1].AwaitStart()

		t.awaitRouteAgent(nil)

		Expect(t.handler.Stop()).To(Succeed())

		t.pingerMap[healthCheckIP1].AwaitStop()

		Eventually(func(g Gomega) {
			_, err := t.client.Get(context.TODO(), localNodeName, metav1.GetOptions{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}).Within(5 * time.Second).Should(Succeed())

		Expect(t.handler.Stop()).To(Succeed())
	})
})

type testDriver struct {
	*eventtesting.ControllerSupport
	supportedIPFamilies  []k8snet.IPFamily
	pingerMap            map[string]*fake.Pinger
	handler              event.Handler
	endpoints            dynamic.ResourceInterface
	client               submarinerv1client.RouteAgentInterface
	healthcheckerEnabled bool
}

func newTestDriver() *testDriver {
	t := &testDriver{
		ControllerSupport: eventtesting.NewControllerSupport(),
	}

	BeforeEach(func() {
		t.supportedIPFamilies = []k8snet.IPFamily{k8snet.IPv4}
		t.healthcheckerEnabled = true

		clientset := fakeClient.NewSimpleClientset()

		dynamicClient := dynamicfake.NewSimpleDynamicClient(kubeScheme.Scheme)

		t.endpoints = dynamicClient.Resource(submarinerv1.SchemeGroupVersion.WithResource("endpoints")).Namespace(namespace)
		t.client = clientset.SubmarinerV1().RouteAgents(namespace)
		t.pingerMap = map[string]*fake.Pinger{
			healthCheckIP1: fake.NewPinger(healthCheckIP1),
			healthCheckIP2: fake.NewPinger(healthCheckIP2),
		}
	})

	JustBeforeEach(func() {
		config := &healthchecker.Config{
			ControllerConfig: pinger.ControllerConfig{
				SupportedIPFamilies: t.supportedIPFamilies,
				PingInterval:        1, // Set interval to 1 second for faster testing
				MaxPacketLossCount:  1,
				NewPinger: func(pingerCfg pinger.Config) pinger.Interface {
					defer GinkgoRecover()
					p, ok := t.pingerMap[pingerCfg.IP]
					Expect(ok).To(BeTrue())

					return p
				},
			},
			HealthCheckerEnabled:     t.healthcheckerEnabled,
			RouteAgentUpdateInterval: 100 * time.Millisecond,
		}

		t.handler = healthchecker.New(config, t.client, "v1", localNodeName)

		t.Start(t.handler)
	})

	return t
}

func (t *testDriver) newSubmEndpoint(healthCheckIPs ...string) *submarinerv1.Endpoint {
	endpointSpec := &submarinerv1.EndpointSpec{
		ClusterID:      remoteClusterID,
		CableName:      fmt.Sprintf("submariner-cable-%s-192-68-1-20", remoteClusterID),
		HealthCheckIPs: healthCheckIPs,
	}

	for _, ip := range healthCheckIPs {
		endpointSpec.Subnets = append(endpointSpec.Subnets, ip+"/24")
	}

	endpointName, err := endpointSpec.GenerateName()
	Expect(err).To(Succeed())

	endpoint := &submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name:              endpointName,
			CreationTimestamp: metav1.Now(),
		},
		Spec: *endpointSpec,
	}

	return endpoint
}

func (t *testDriver) newLatencyInfo(family k8snet.IPFamily) *pinger.LatencyInfo {
	return &pinger.LatencyInfo{
		ConnectionStatus: pinger.Connected,
		Spec: &submarinerv1.LatencyRTTSpec{
			Last:    string(family) + "82ms",
			Min:     string(family) + "80ms",
			Average: string(family) + "85ms",
			Max:     string(family) + "89ms",
			StdDev:  string(family) + "5ms",
		},
	}
}

func (t *testDriver) setLatencyInfo(ip string, latencyInfo *pinger.LatencyInfo) {
	pingerObject := t.pingerMap[ip]
	pingerObject.SetLatencyInfo(latencyInfo)
}

func (t *testDriver) Start(handler event.Handler) {
	t.ControllerSupport.Start(handler)
}

func (t *testDriver) awaitRouteAgent(verify func(*submarinerv1.RouteAgent, Gomega)) {
	Eventually(func(g Gomega) {
		ra, err := t.client.Get(context.TODO(), localNodeName, metav1.GetOptions{})
		g.Expect(err).ToNot(HaveOccurred(), "Error retrieving RouteAgent")

		if verify != nil {
			verify(ra, g)
		}
	}).Within(5 * time.Second).Should(Succeed())
}

func (t *testDriver) awaitRemoteEndpoint(verify func(*submarinerv1.RemoteEndpoint, Gomega)) {
	t.awaitRouteAgent(func(ra *submarinerv1.RouteAgent, g Gomega) {
		g.Expect(ra.Status.RemoteEndpoints).ToNot(BeEmpty())

		if verify != nil {
			verify(&ra.Status.RemoteEndpoints[0], g)
		}
	})
}
