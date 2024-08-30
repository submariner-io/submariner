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
	"errors"
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
	"k8s.io/apimachinery/pkg/util/wait"
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
		t.awaitRouteAgent(nil)
	})

	When("a remote Endpoint is created/updated/deleted", func() {
		It("should add/update/delete its RemoteEndpoint information in the RouteAgent resource", func() {
			endpoint1 := t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))

			remoteEndpoint := t.awaitRemoteEndpoint(nil)
			Expect(remoteEndpoint.Spec).To(Equal(endpoint1.Spec))

			By("Updating remote endpoint")

			endpoint1.Spec.Hostname = "newHostName"
			t.UpdateEndpoint(endpoint1)

			remoteEndpoint = t.awaitRemoteEndpoint(func(ep *submarinerv1.RemoteEndpoint) bool {
				return ep.Spec.Hostname == endpoint1.Spec.Hostname
			})
			Expect(remoteEndpoint.Spec).To(Equal(endpoint1.Spec))

			By("Deleting remote endpoint")

			t.DeleteEndpoint(endpoint1.Name)

			t.awaitRouteAgent(func(ra *submarinerv1.RouteAgent) bool {
				return len(ra.Status.RemoteEndpoints) == 0
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

			latencyInfo := t.newLatencyInfo()
			t.setLatencyInfo(healthCheckIP1, latencyInfo)

			remoteEndpoint := t.awaitRemoteEndpoint(func(ep *submarinerv1.RemoteEndpoint) bool {
				return ep.Status == submarinerv1.Connected
			})

			Expect(remoteEndpoint.Status).To(Equal(submarinerv1.Connected))
			Expect(remoteEndpoint.LatencyRTT).To(Equal(latencyInfo.Spec))
		})

		Context("with no HealthCheckIP", func() {
			It("should not start a pinger and should set the RemoteEndpoint Status to None", func() {
				endpoint1 := t.CreateEndpoint(t.newSubmEndpoint(""))
				t.pingerMap[healthCheckIP1].AwaitNoStart()

				remoteEndpoint := t.awaitRemoteEndpoint(func(ep *submarinerv1.RemoteEndpoint) bool {
					return ep.Status == submarinerv1.ConnectionNone
				})

				Expect(remoteEndpoint.Status).To(Equal(submarinerv1.ConnectionNone))
				Expect(remoteEndpoint.Spec).To(Equal(endpoint1.Spec))
			})
		})

		Context("on the gateway", func() {
			It("should not start a pinger and should set the RemoteEndpoint Status to None", func() {
				_ = t.CreateLocalHostEndpoint()
				endpoint1 := t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))
				t.pingerMap[healthCheckIP1].AwaitNoStart()

				remoteEndpoint := t.awaitRemoteEndpoint(func(ep *submarinerv1.RemoteEndpoint) bool {
					return ep.Status == submarinerv1.ConnectionNone
				})

				Expect(remoteEndpoint.Status).To(Equal(submarinerv1.ConnectionNone))
				Expect(remoteEndpoint.Spec).To(Equal(endpoint1.Spec))
			})
		})

		Context("with health check not enabled", func() {
			BeforeEach(func() {
				t.healthcheckerEnabled = false
			})

			It("should not start a pinger and should set the RemoteEndpoint Status to None", func() {
				endpoint1 := t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))
				t.pingerMap[healthCheckIP1].AwaitNoStart()

				remoteEndpoint := t.awaitRemoteEndpoint(func(ep *submarinerv1.RemoteEndpoint) bool {
					return ep.Status == submarinerv1.ConnectionNone
				})

				Expect(remoteEndpoint.Status).To(Equal(submarinerv1.ConnectionNone))
				Expect(remoteEndpoint.Spec).To(Equal(endpoint1.Spec))
			})
		})
	})

	When("a remote Endpoint is updated", func() {
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
		It("should stop the pinger", func() {
			endpoint1 := t.CreateEndpoint(t.newSubmEndpoint(healthCheckIP1))
			t.pingerMap[healthCheckIP1].AwaitStart()

			t.DeleteEndpoint(endpoint1.Name)
			t.pingerMap[healthCheckIP1].AwaitStop()
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

			remoteEndpoint := t.awaitRemoteEndpoint(func(ep *submarinerv1.RemoteEndpoint) bool {
				return ep.Status == submarinerv1.ConnectionError
			})

			Expect(remoteEndpoint.Status).To(Equal(submarinerv1.ConnectionError))
			Expect(remoteEndpoint.StatusMessage).To(Equal(latencyInfo.ConnectionError))
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
		ControllerSupport: eventtesting.NewControllerSupport(),
	}

	BeforeEach(func() {
		t.stopCh = make(chan struct{})
		t.healthcheckerEnabled = true

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
			Expect(pingerCfg.Interval).To(Equal(time.Second * time.Duration(config.PingInterval)))
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

func (t *testDriver) awaitRouteAgent(verify func(*submarinerv1.RouteAgent) bool) *submarinerv1.RouteAgent {
	var routeAgent *submarinerv1.RouteAgent

	_ = wait.PollUntilContextTimeout(context.TODO(), 20*time.Millisecond,
		5*time.Second, true, func(ctx context.Context) (bool, error) {
			ra, err := t.client.Get(ctx, localNodeName, metav1.GetOptions{})
			if apierrors.IsNotFound(err) || errors.Is(err, context.DeadlineExceeded) {
				return false, nil
			}

			Expect(err).ToNot(HaveOccurred(), "Error retrieving RouteAgent")

			routeAgent = ra

			return verify == nil || verify(routeAgent), nil
		})

	return routeAgent
}

func (t *testDriver) awaitRemoteEndpoint(verify func(*submarinerv1.RemoteEndpoint) bool) *submarinerv1.RemoteEndpoint {
	routeAgent := t.awaitRouteAgent(func(ra *submarinerv1.RouteAgent) bool {
		return len(ra.Status.RemoteEndpoints) != 0 && (verify == nil || verify(&ra.Status.RemoteEndpoints[0]))
	})

	return &routeAgent.Status.RemoteEndpoints[0]
}
