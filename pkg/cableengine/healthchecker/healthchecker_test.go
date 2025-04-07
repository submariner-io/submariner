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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cableengine/healthchecker"
	"github.com/submariner-io/submariner/pkg/pinger"
	"github.com/submariner-io/submariner/pkg/pinger/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	fakeClient "k8s.io/client-go/dynamic/fake"
	kubeScheme "k8s.io/client-go/kubernetes/scheme"
	k8snet "k8s.io/utils/net"
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
		healthChecker       healthchecker.Interface
		supportedIPFamilies []k8snet.IPFamily
		endpoints           dynamic.ResourceInterface
		pingerMap           map[string]*fake.Pinger
		stopCh              chan struct{}
	)

	BeforeEach(func() {
		supportedIPFamilies = []k8snet.IPFamily{k8snet.IPv4}
		pingerMap = map[string]*fake.Pinger{
			healthCheckIP1: fake.NewPinger(healthCheckIP1),
			healthCheckIP2: fake.NewPinger(healthCheckIP2),
		}
	})

	JustBeforeEach(func() {
		stopCh = make(chan struct{})

		dynamicClient := fakeClient.NewSimpleDynamicClient(kubeScheme.Scheme)
		restMapper := test.GetRESTMapperFor(&submarinerv1.Endpoint{})
		endpoints = dynamicClient.Resource(*test.GetGroupVersionResourceFor(restMapper, &submarinerv1.Endpoint{})).Namespace(namespace)

		var err error

		config := &healthchecker.Config{
			ControllerConfig: pinger.ControllerConfig{
				SupportedIPFamilies: supportedIPFamilies,
				PingInterval:        3,
				MaxPacketLossCount:  4,
				NewPinger: func(pingerCfg pinger.Config) pinger.Interface {
					p, ok := pingerMap[pingerCfg.IP]
					Expect(ok).To(BeTrue())

					return p
				},
			},
			WatcherConfig: watcher.Config{
				RestMapper: restMapper,
				Client:     dynamicClient,
			},
			EndpointNamespace: namespace,
			ClusterID:         localClusterID,
		}

		healthChecker, err = healthchecker.New(config)
		Expect(err).ToNot(HaveOccurred())
		Expect(healthChecker.Start(stopCh)).To(Succeed())
	})

	AfterEach(func() {
		close(stopCh)
	})

	createEndpoint := func(clusterID string, healthCheckIPs ...string) *submarinerv1.Endpoint {
		endpointSpec := &submarinerv1.EndpointSpec{
			ClusterID:      clusterID,
			CableName:      fmt.Sprintf("submariner-cable-%s-192-68-1-20", clusterID),
			HealthCheckIPs: healthCheckIPs,
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

	newLatencyInfo := func() *pinger.LatencyInfo {
		return &pinger.LatencyInfo{
			ConnectionStatus: pinger.Connected,
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
		var (
			endpoint1 *submarinerv1.Endpoint
			endpoint2 *submarinerv1.Endpoint
		)

		JustBeforeEach(func() {
			endpoint1 = createEndpoint(remoteClusterID1, healthCheckIP1)
			pingerMap[healthCheckIP1].AwaitStart()

			endpoint2 = createEndpoint(remoteClusterID2, healthCheckIP2)
			pingerMap[healthCheckIP2].AwaitStart()
		})

		It("should start a Pinger and return the correct LatencyInfo", func() {
			latencyInfo1 := newLatencyInfo()
			pingerMap[healthCheckIP1].SetLatencyInfo(latencyInfo1)
			Eventually(func() *pinger.LatencyInfo {
				return healthChecker.GetLatencyInfo(&endpoint1.Spec, k8snet.IPv4)
			}).Should(Equal(latencyInfo1))

			latencyInfo2 := &pinger.LatencyInfo{
				ConnectionStatus: pinger.ConnectionError,
				Spec: &submarinerv1.LatencyRTTSpec{
					Last:    "82ms",
					Min:     "80ms",
					Average: "85ms",
					Max:     "89ms",
					StdDev:  "5ms",
				},
			}

			pingerMap[healthCheckIP2].SetLatencyInfo(latencyInfo2)
			Eventually(func() *pinger.LatencyInfo {
				return healthChecker.GetLatencyInfo(&endpoint2.Spec, k8snet.IPv4)
			}).Should(Equal(latencyInfo2))
		})

		Context("and subsequently deleted", func() {
			It("should stop the Pinger", func() {
				Expect(endpoints.Delete(context.TODO(), endpoint1.Name, metav1.DeleteOptions{})).To(Succeed())
				pingerMap[healthCheckIP1].AwaitStop()
				Eventually(func() *pinger.LatencyInfo {
					return healthChecker.GetLatencyInfo(&endpoint1.Spec, k8snet.IPv4)
				}).Should(BeNil())
			})
		})

		Context("and the health checker is subsequently restarted", func() {
			It("should restart the Pinger", func() {
				By("Stopping health checker")

				close(stopCh)
				healthChecker.Stop()

				Expect(healthChecker.GetLatencyInfo(&endpoint1.Spec, k8snet.IPv4)).To(BeNil())
				Expect(healthChecker.GetLatencyInfo(&endpoint2.Spec, k8snet.IPv4)).To(BeNil())

				By("Restarting health checker")

				pingerMap = map[string]*fake.Pinger{
					healthCheckIP1: fake.NewPinger(healthCheckIP1),
					healthCheckIP2: fake.NewPinger(healthCheckIP2),
				}

				stopCh = make(chan struct{})
				Expect(healthChecker.Start(stopCh)).To(Succeed())

				pingerMap[healthCheckIP1].AwaitStart()
				pingerMap[healthCheckIP2].AwaitStart()
			})
		})
	})

	When("a remote Endpoint is created/deleted with dual-stack health check IPs", func() {
		const healthCheckIPv6 = "2001:db8:3333:4444:5555:6666:7777:8888"

		BeforeEach(func() {
			supportedIPFamilies = []k8snet.IPFamily{k8snet.IPv4, k8snet.IPv6}
			pingerMap[healthCheckIPv6] = fake.NewPinger(healthCheckIPv6)
		})

		It("should start/stop Pingers and return the correct LatencyInfo for both", func() {
			endpoint := createEndpoint(remoteClusterID1, healthCheckIP1, healthCheckIPv6)
			pingerMap[healthCheckIP1].AwaitStart()
			pingerMap[healthCheckIPv6].AwaitStart()

			ipv4LatencyInfo := newLatencyInfo()
			pingerMap[healthCheckIP1].SetLatencyInfo(ipv4LatencyInfo)
			Eventually(func() *pinger.LatencyInfo {
				return healthChecker.GetLatencyInfo(&endpoint.Spec, k8snet.IPv4)
			}).Should(Equal(ipv4LatencyInfo))

			ipv6LatencyInfo := &pinger.LatencyInfo{
				ConnectionStatus: pinger.ConnectionError,
				Spec: &submarinerv1.LatencyRTTSpec{
					Last:    "82ms",
					Min:     "80ms",
					Average: "85ms",
					Max:     "89ms",
					StdDev:  "5ms",
				},
			}

			pingerMap[healthCheckIPv6].SetLatencyInfo(ipv6LatencyInfo)
			Eventually(func() *pinger.LatencyInfo {
				return healthChecker.GetLatencyInfo(&endpoint.Spec, k8snet.IPv6)
			}).Should(Equal(ipv6LatencyInfo))

			By("Deleting Endpoint")

			Expect(endpoints.Delete(context.TODO(), endpoint.Name, metav1.DeleteOptions{})).To(Succeed())

			pingerMap[healthCheckIP1].AwaitStop()
			Eventually(func() *pinger.LatencyInfo {
				return healthChecker.GetLatencyInfo(&endpoint.Spec, k8snet.IPv4)
			}).Should(BeNil())

			pingerMap[healthCheckIPv6].AwaitStop()
			Eventually(func() *pinger.LatencyInfo {
				return healthChecker.GetLatencyInfo(&endpoint.Spec, k8snet.IPv6)
			}).Should(BeNil())
		})
	})

	When("a local Endpoint is created", func() {
		It("should not start a Pinger", func() {
			createEndpoint(localClusterID, healthCheckIP1)
			pingerMap[healthCheckIP1].AwaitNoStart()
		})
	})

	When("a remote Endpoint is updated and the HealthCheckIP was changed", func() {
		var endpoint *submarinerv1.Endpoint

		BeforeEach(func() {
			pingerMap[healthCheckIP3] = fake.NewPinger(healthCheckIP3)
		})

		JustBeforeEach(func() {
			endpoint = createEndpoint(remoteClusterID1, healthCheckIP1)
			pingerMap[healthCheckIP1].AwaitStart()
		})

		It("should stop the Pinger and start a new one", func() {
			endpoint.Spec.HealthCheckIPs = []string{healthCheckIP3}

			test.UpdateResource(endpoints, endpoint)
			pingerMap[healthCheckIP1].AwaitStop()
			pingerMap[healthCheckIP3].AwaitStart()

			latencyInfo := newLatencyInfo()
			pingerMap[healthCheckIP3].SetLatencyInfo(latencyInfo)
			Eventually(func() *pinger.LatencyInfo {
				return healthChecker.GetLatencyInfo(&endpoint.Spec, k8snet.IPv4)
			}).Should(Equal(latencyInfo))
		})
	})
})
