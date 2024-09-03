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
		var routeAgent *submarinerv1.RouteAgent
		Eventually(func() bool {
			var err error
			routeAgent, err = t.client.Get(context.TODO(), localNodeName, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return false
			}

			Expect(err).To(Succeed())
			return true
		}).Should(BeTrue())
		Expect(routeAgent.Status.RemoteEndpoints).To(BeEmpty())
	})

	When("a remote Endpoint is created/updated/deleted", func() {
		It("should add/update/delete its RemoteEndpoint information to the RouteAgent resource", func() {
			endpoint1 := t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))

			var routeAgent *submarinerv1.RouteAgent
			Eventually(func() bool {
				var err error
				routeAgent, err = t.client.Get(context.TODO(), localNodeName, metav1.GetOptions{})
				if apierrors.IsNotFound(err) {
					return false
				}

				Expect(err).To(Succeed())
				return len(routeAgent.Status.RemoteEndpoints) != 0
			}).Should(BeTrue())

			remoteEndpoint := routeAgent.Status.RemoteEndpoints[0]
			Expect(remoteEndpoint.Spec).To(Equal(endpoint1.Spec))

			endpoint1.Spec.Hostname = "newHostName"
			t.UpdateEndpoint(endpoint1)

			Eventually(func() string {
				var err error
				routeAgent, err = t.client.Get(context.TODO(), localNodeName, metav1.GetOptions{})
				if apierrors.IsNotFound(err) {
					return ""
				}

				Expect(err).To(Succeed())
				if len(routeAgent.Status.RemoteEndpoints) != 0 {
					return routeAgent.Status.RemoteEndpoints[0].Spec.Hostname
				}
				return ""
			}).Should(Equal(endpoint1.Spec.Hostname))

			remoteEndpoint = routeAgent.Status.RemoteEndpoints[0]
			Expect(remoteEndpoint.Spec).To(Equal(endpoint1.Spec))

			t.DeleteEndpoint(endpoint1.Name)
			Eventually(func() bool {
				var err error
				routeAgent, err = t.client.Get(context.TODO(), localNodeName, metav1.GetOptions{})
				if apierrors.IsNotFound(err) {
					return false
				}

				Expect(err).To(Succeed())
				return len(routeAgent.Status.RemoteEndpoints) == 0
			}).Should(BeTrue())
		})
	})
})

var _ = Describe("RemoteEndpoint latency info", func() {
	When("a remote Endpoint is created", func() {
		t := newTestDriver()
		It("should start a pinger and correctly update the RemoteEndpoint Status and LatencyInfo", func() {
			endpoint1 := t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))
			t.pingerMap[healthCheckIP1].AwaitStart()

			latencyInfo := t.newLatencyInfo()
			t.setLatencyInfo(healthCheckIP1, latencyInfo)

			var routeAgent *submarinerv1.RouteAgent
			Eventually(func() *submarinerv1.LatencyRTTSpec {
				var err error
				routeAgent, err = t.client.Get(context.TODO(), localNodeName, metav1.GetOptions{})
				if apierrors.IsNotFound(err) {
					return nil
				}

				Expect(err).To(Succeed())
				if len(routeAgent.Status.RemoteEndpoints) != 0 && routeAgent.Status.RemoteEndpoints[0].LatencyRTT != nil {
					return routeAgent.Status.RemoteEndpoints[0].LatencyRTT
				}
				return nil
			}).Should(Equal(latencyInfo.Spec))

			remoteEndpoint := routeAgent.Status.RemoteEndpoints[0]
			Expect(remoteEndpoint.Spec).To(Equal(endpoint1.Spec))
			Expect(remoteEndpoint.Status).To(Equal(submarinerv1.Connected))
		})
		Context("with no HealthCheckIP", func() {
			It("should not start a pinger and should set the RemoteEndpoint Status to None", func() {
				endpoint1 := t.CreateEndpoint(t.newSubmEndpoint(""))
				t.pingerMap[healthCheckIP1].AwaitNoStart()

				var routeAgent *submarinerv1.RouteAgent
				Eventually(func() submarinerv1.ConnectionStatus {
					var err error
					routeAgent, err = t.client.Get(context.TODO(), localNodeName, metav1.GetOptions{})
					if apierrors.IsNotFound(err) {
						return submarinerv1.Connecting
					}

					Expect(err).To(Succeed())
					if err == nil && len(routeAgent.Status.RemoteEndpoints) != 0 {
						return routeAgent.Status.RemoteEndpoints[0].Status
					}
					return submarinerv1.Connecting
				}).Should(Equal(submarinerv1.ConnectionNone))

				remoteEndpoint := routeAgent.Status.RemoteEndpoints[0]
				Expect(remoteEndpoint.Spec).To(Equal(endpoint1.Spec))
				Expect(remoteEndpoint.Status).To(Equal(submarinerv1.ConnectionNone))
			})
		})
		Context("on the gateway", func() {
			It("should not start a pinger and should set the RemoteEndpoint Status to None", func() {
				_ = t.CreateLocalHostEndpoint()
				endpoint1 := t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))
				t.pingerMap[healthCheckIP1].AwaitNoStart()

				var routeAgent *submarinerv1.RouteAgent
				Eventually(func() submarinerv1.ConnectionStatus {
					var err error
					routeAgent, err = t.client.Get(context.TODO(), localNodeName, metav1.GetOptions{})
					if apierrors.IsNotFound(err) {
						return submarinerv1.Connecting
					}

					Expect(err).To(Succeed())
					if len(routeAgent.Status.RemoteEndpoints) != 0 {
						return routeAgent.Status.RemoteEndpoints[0].Status
					}
					return submarinerv1.Connecting
				}).Should(Equal(submarinerv1.ConnectionNone))

				remoteEndpoint := routeAgent.Status.RemoteEndpoints[0]
				Expect(remoteEndpoint.Spec).To(Equal(endpoint1.Spec))
				Expect(remoteEndpoint.Status).To(Equal(submarinerv1.ConnectionNone))
			})
		})

		Context("with health check not enabled", func() {
			t := newTestDriver()

			BeforeEach(func() {
				t.healthcheckerEnabled = false
			})

			It("should not start a pinger and should set the RemoteEndpoint Status to None", func() {
				endpoint1 := t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))
				t.pingerMap[healthCheckIP1].AwaitNoStart()

				var routeAgent *submarinerv1.RouteAgent
				Eventually(func() submarinerv1.ConnectionStatus {
					var err error
					routeAgent, err = t.client.Get(context.TODO(), localNodeName, metav1.GetOptions{})
					if apierrors.IsNotFound(err) {
						return submarinerv1.Connecting
					}

					Expect(err).To(Succeed())
					if err == nil && len(routeAgent.Status.RemoteEndpoints) != 0 {
						return routeAgent.Status.RemoteEndpoints[0].Status
					}
					return submarinerv1.Connecting
				}).Should(Equal(submarinerv1.ConnectionNone))

				remoteEndpoint := routeAgent.Status.RemoteEndpoints[0]
				Expect(remoteEndpoint.Spec).To(Equal(endpoint1.Spec))
				Expect(remoteEndpoint.Status).To(Equal(submarinerv1.ConnectionNone))
			})
		})
	})

	When("a remote Endpoint is updated", func() {
		t := newTestDriver()
		Context("and the HealthCheckIP was changed", func() {
			It("should stop the pinger and start a new one", func() {
				endpoint1 := t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))

				t.pingerMap[healthCheckIP1].AwaitStart()
				t.pingerMap[healthCheckIP2] = fake.NewPinger(healthCheckIP2)

				endpoint1.Spec.HealthCheckIP = healthCheckIP2

				t.UpdateEndpoint(endpoint1)
				t.pingerMap[healthCheckIP1].AwaitStop()
				t.pingerMap[healthCheckIP2].AwaitStart()
			})
		})
		Context("and the HealthCheckIP did not change", func() {
			It("should not start a new pinger", func() {
				endpoint1 := t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))
				t.pingerMap[healthCheckIP1].AwaitStart()

				endpoint1.Spec.Hostname = "newHostName"
				t.UpdateEndpoint(endpoint1)

				pingerObject, found := t.pingerMap[endpoint1.Spec.HealthCheckIP]
				Expect(found).To(BeTrue())
				Expect(pingerObject.GetIP()).To(Equal(healthCheckIP1))

				t.pingerMap[healthCheckIP1].AwaitNoStop()
			})
		})
	})

	When("a remote Endpoint is deleted", func() {
		t := newTestDriver()
		It("should stop the pinger", func() {
			endpoint1 := t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))
			t.pingerMap[healthCheckIP1].AwaitStart()

			t.DeleteEndpoint(endpoint1.Name)
			t.pingerMap[healthCheckIP1].AwaitStop()
		})
	})
})

var _ = Describe("Transition of nodes", func() {
	When("a node transition to gateway node", func() {
		t := newTestDriver()
		It("should stop the pinger", func() {
			_ = t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))
			t.pingerMap[healthCheckIP1].AwaitStart()

			_ = t.CreateLocalHostEndpoint()
			t.pingerMap[healthCheckIP1].AwaitStop()
		})
	})

	When("a node transition to non-gateway node", func() {
		t := newTestDriver()
		It("should start the pinger", func() {
			endpoint := t.CreateLocalHostEndpoint()
			_ = t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))
			t.pingerMap[healthCheckIP1].AwaitNoStart()

			t.DeleteEndpoint(endpoint.Name)
			t.pingerMap[healthCheckIP1].AwaitStart()
		})
	})
})

type testDriver struct {
	*eventtesting.ControllerSupport
	pingerMap            map[string]*fake.Pinger
	handler              event.Handler
	endpoints            dynamic.ResourceInterface
	client               submarinerv1client.RouteAgentInterface
	stopCh               chan struct{}
	healthcheckerEnabled bool
}

func newTestDriver() *testDriver {
	t := &testDriver{
		ControllerSupport:    eventtesting.NewControllerSupport(),
		healthcheckerEnabled: true,
	}

	BeforeEach(func() {
		t.stopCh = make(chan struct{})

		clientset := fakeClient.NewSimpleClientset()

		dynamicClient := dynamicfake.NewSimpleDynamicClient(kubeScheme.Scheme)

		t.endpoints = dynamicClient.Resource(submarinerv1.SchemeGroupVersion.WithResource("endpoints")).Namespace(namespace)
		t.client = clientset.SubmarinerV1().RouteAgents(namespace)
		t.pingerMap = map[string]*fake.Pinger{
			healthCheckIP1: fake.NewPinger(healthCheckIP1),
		}
	})

	JustBeforeEach(func() {
		config := &healthchecker.Config{
			PingInterval:             1, // Set interval to 1 second for faster testing
			MaxPacketLossCount:       1,
			HealthCheckerEnabled:     t.healthcheckerEnabled,
			RouteAgentUpdateInterval: 100 * time.Millisecond,
		}

		config.NewPinger = func(pingerCfg pinger.Config) pinger.Interface {
			defer GinkgoRecover()
			Expect(pingerCfg.Interval).To(Equal(time.Second *
				time.Duration(config.PingInterval))) //nolint:gosec // We can safely ignore integer conversion error
			Expect(pingerCfg.MaxPacketLossCount).To(Equal(config.MaxPacketLossCount))

			p, ok := t.pingerMap[pingerCfg.IP]
			Expect(ok).To(BeTrue())

			return p
		}
		t.handler = healthchecker.New(config, t.client, "v1", localNodeName)

		t.Start(t.handler)
	})

	AfterEach(func() {
		close(t.stopCh)
	})

	return t
}

func (t *testDriver) newSubmEndpoint(healthCheckIP string) *submarinerv1.Endpoint {
	endpointSpec := &submarinerv1.EndpointSpec{
		ClusterID: remoteClusterID,
		CableName: fmt.Sprintf("submariner-cable-%s-192-68-1-20", remoteClusterID),
	}
	endpointSpec.HealthCheckIP = healthCheckIP

	endpointName, err := endpointSpec.GenerateName()
	Expect(err).To(Succeed())

	endpoint := &submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: endpointName,
		},
		Spec: *endpointSpec,
	}

	return endpoint
}

func (t *testDriver) newLatencyInfo() *pinger.LatencyInfo {
	return &pinger.LatencyInfo{
		ConnectionStatus: pinger.Connected,
		Spec: &submarinerv1.LatencyRTTSpec{
			Last:    "82ms",
			Min:     "80ms",
			Average: "85ms",
			Max:     "89ms",
			StdDev:  "5ms",
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
