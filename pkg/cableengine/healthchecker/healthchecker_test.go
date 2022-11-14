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

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cableengine/healthchecker"
	"github.com/submariner-io/submariner/pkg/cableengine/healthchecker/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	fakeClient "k8s.io/client-go/dynamic/fake"
	kubeScheme "k8s.io/client-go/kubernetes/scheme"
)

var _ = Describe("Controller", func() {
	const namespace = "submariner"
	const localClusterID = "east"
	const remoteClusterID1 = "west"
	const remoteClusterID2 = "north"
	const healthCheckIP1 = "1.1.1.1"
	const healthCheckIP2 = "2.2.2.2"
	const healthCheckIP3 = "3.3.3.3"

	var (
		healthChecker healthchecker.Interface
		endpoints     dynamic.ResourceInterface
		pingerMap     map[string]*fake.Pinger
		stopCh        chan struct{}
	)

	BeforeEach(func() {
		pingerMap = map[string]*fake.Pinger{
			healthCheckIP1: fake.NewPinger(healthCheckIP1),
			healthCheckIP2: fake.NewPinger(healthCheckIP2),
		}
	})

	JustBeforeEach(func() {
		stopCh = make(chan struct{})
		scheme := runtime.NewScheme()
		Expect(submarinerv1.AddToScheme(scheme)).To(Succeed())
		Expect(submarinerv1.AddToScheme(kubeScheme.Scheme)).To(Succeed())

		dynamicClient := fakeClient.NewSimpleDynamicClient(scheme)
		restMapper := test.GetRESTMapperFor(&submarinerv1.Endpoint{})
		endpoints = dynamicClient.Resource(*test.GetGroupVersionResourceFor(restMapper, &submarinerv1.Endpoint{})).Namespace(namespace)

		var err error

		config := &healthchecker.Config{
			WatcherConfig: &watcher.Config{
				RestMapper: restMapper,
				Client:     dynamicClient,
				Scheme:     scheme,
			},
			EndpointNamespace:  namespace,
			ClusterID:          localClusterID,
			PingInterval:       3,
			MaxPacketLossCount: 4,
		}

		config.NewPinger = func(pingerCfg healthchecker.PingerConfig) healthchecker.PingerInterface {
			defer GinkgoRecover()
			Expect(pingerCfg.Interval).To(Equal(time.Second * time.Duration(config.PingInterval)))
			Expect(pingerCfg.MaxPacketLossCount).To(Equal(config.MaxPacketLossCount))

			p, ok := pingerMap[pingerCfg.IP]
			Expect(ok).To(BeTrue())
			return p
		}

		healthChecker, err = healthchecker.New(config)

		Expect(err).To(Succeed())
		Expect(healthChecker.Start(stopCh)).To(Succeed())
	})

	AfterEach(func() {
		close(stopCh)
	})

	createEndpoint := func(clusterID, healthCheckIP string) *submarinerv1.Endpoint {
		endpointSpec := &submarinerv1.EndpointSpec{
			ClusterID:     clusterID,
			CableName:     fmt.Sprintf("submariner-cable-%s-192-68-1-20", clusterID),
			HealthCheckIP: healthCheckIP,
		}

		endpointName, err := endpointSpec.GenerateName()
		Expect(err).To(Succeed())

		endpoint := &submarinerv1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name: endpointName,
			},
			Spec: *endpointSpec,
		}

		test.CreateResource(endpoints, endpoint)

		return endpoint
	}

	newLatencyInfo := func() *healthchecker.LatencyInfo {
		return &healthchecker.LatencyInfo{
			ConnectionStatus: healthchecker.Connected,
			Spec: &submarinerv1.LatencyRTTSpec{
				Last:    "93ms",
				Min:     "90ms",
				Average: "95ms",
				Max:     "100ms",
				StdDev:  "94ms",
			},
		}
	}

	When("a remote Endpoint is created", func() {
		It("should start a Pinger and return the correct LatencyInfo", func() {
			endpoint1 := createEndpoint(remoteClusterID1, healthCheckIP1)
			pingerMap[healthCheckIP1].AwaitStart()

			endpoint2 := createEndpoint(remoteClusterID2, healthCheckIP2)
			pingerMap[healthCheckIP2].AwaitStart()

			latencyInfo1 := newLatencyInfo()
			pingerMap[healthCheckIP1].SetLatencyInfo(latencyInfo1)
			Eventually(healthChecker.GetLatencyInfo(&endpoint1.Spec)).Should(Equal(latencyInfo1))

			latencyInfo2 := &healthchecker.LatencyInfo{
				ConnectionStatus: healthchecker.ConnectionError,
				Spec: &submarinerv1.LatencyRTTSpec{
					Last:    "82ms",
					Min:     "80ms",
					Average: "85ms",
					Max:     "89ms",
					StdDev:  "5ms",
				},
			}

			pingerMap[healthCheckIP2].SetLatencyInfo(latencyInfo2)
			Eventually(healthChecker.GetLatencyInfo(&endpoint2.Spec)).Should(Equal(latencyInfo2))
		})
	})

	When("a local Endpoint is created", func() {
		It("should not start a Pinger", func() {
			createEndpoint(localClusterID, healthCheckIP1)
			pingerMap[healthCheckIP1].AwaitNoStart()
		})
	})

	When("a remote Endpoint is deleted", func() {
		It("should stop the Pinger", func() {
			endpoint := createEndpoint(remoteClusterID1, healthCheckIP1)
			pingerMap[healthCheckIP1].AwaitStart()

			Expect(endpoints.Delete(context.TODO(), endpoint.Name, metav1.DeleteOptions{})).To(Succeed())
			pingerMap[healthCheckIP1].AwaitStop()
			Eventually(healthChecker.GetLatencyInfo(&endpoint.Spec)).Should(BeNil())
		})
	})

	When("a remote Endpoint is updated", func() {
		var endpoint *submarinerv1.Endpoint

		JustBeforeEach(func() {
			endpoint = createEndpoint(remoteClusterID1, healthCheckIP1)
			pingerMap[healthCheckIP1].AwaitStart()
		})

		When("the HealthCheckIP was changed", func() {
			BeforeEach(func() {
				pingerMap[healthCheckIP3] = fake.NewPinger(healthCheckIP3)
			})

			It("should stop the Pinger and start a new one", func() {
				endpoint.Spec.HealthCheckIP = healthCheckIP3

				test.UpdateResource(endpoints, endpoint)
				pingerMap[healthCheckIP1].AwaitStop()
				pingerMap[healthCheckIP3].AwaitStart()

				latencyInfo := newLatencyInfo()
				pingerMap[healthCheckIP3].SetLatencyInfo(latencyInfo)
				Eventually(healthChecker.GetLatencyInfo(&endpoint.Spec)).Should(Equal(latencyInfo))
			})
		})

		When("the HealthCheckIP did not changed", func() {
			It("should not start a new Pinger", func() {
				endpoint.Spec.Hostname = "raiders"

				test.UpdateResource(endpoints, endpoint)
				pingerMap[healthCheckIP1].AwaitNoStop()
			})
		})
	})

	When("a remote Endpoint is has no HealthCheckIP", func() {
		It("should not start a Pinger", func() {
			createEndpoint(remoteClusterID1, "")
			pingerMap[healthCheckIP1].AwaitNoStart()
		})
	})
})
