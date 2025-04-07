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

package pinger_test

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/pinger"
	"github.com/submariner-io/submariner/pkg/pinger/fake"
	k8snet "k8s.io/utils/net"
)

var _ = Describe("Controller", func() {
	const (
		remoteClusterID1   = "west"
		remoteClusterID2   = "north"
		healthCheckIP1     = "1.1.1.1"
		healthCheckIP2     = "2.2.2.2"
		healthCheckIP3     = "3.3.3.3"
		pingInterval       = 3
		maxPacketLossCount = 4
	)

	var (
		controller          pinger.Controller
		supportedIPFamilies []k8snet.IPFamily
		pingerMap           map[string]*fake.Pinger
	)

	BeforeEach(func() {
		supportedIPFamilies = []k8snet.IPFamily{k8snet.IPv4}
		pingerMap = map[string]*fake.Pinger{
			healthCheckIP1: fake.NewPinger(healthCheckIP1),
			healthCheckIP2: fake.NewPinger(healthCheckIP2),
		}
	})

	JustBeforeEach(func() {
		controller = pinger.NewController(pinger.ControllerConfig{
			SupportedIPFamilies: supportedIPFamilies,
			PingInterval:        pingInterval,
			MaxPacketLossCount:  maxPacketLossCount,
			NewPinger: func(pingerCfg pinger.Config) pinger.Interface {
				defer GinkgoRecover()
				Expect(pingerCfg.Interval).To(Equal(time.Second * time.Duration(pingInterval)))
				Expect(pingerCfg.MaxPacketLossCount).To(Equal(maxPacketLossCount))

				p, ok := pingerMap[pingerCfg.IP]
				Expect(ok).To(BeTrue())
				return p
			},
		})
	})

	When("Endpoints are created/removed", func() {
		It("should start/stop Pingers", func() {
			endpoint1 := newEndpointSpec(remoteClusterID1, healthCheckIP1)
			controller.EndpointCreatedOrUpdated(endpoint1)

			pingerMap[healthCheckIP1].AwaitStart()
			Expect(controller.Get(endpoint1, k8snet.IPv4)).To(Equal(pingerMap[healthCheckIP1]))

			endpoint2 := newEndpointSpec(remoteClusterID2, healthCheckIP2)
			controller.EndpointCreatedOrUpdated(endpoint2)

			pingerMap[healthCheckIP2].AwaitStart()
			Expect(controller.Get(endpoint2, k8snet.IPv4)).To(Equal(pingerMap[healthCheckIP2]))

			By("Removing Endpoints")

			controller.EndpointRemoved(endpoint1)

			pingerMap[healthCheckIP1].AwaitStop()
			Expect(controller.Get(endpoint1, k8snet.IPv4)).To(BeNil())

			controller.EndpointRemoved(endpoint2)

			pingerMap[healthCheckIP2].AwaitStop()
			Expect(controller.Get(endpoint2, k8snet.IPv4)).To(BeNil())
		})
	})

	When("an Endpoint is created/removed with dual-stack health check IPs", func() {
		const healthCheckIPv6 = "2001:db8:3333:4444:5555:6666:7777:8888"

		BeforeEach(func() {
			supportedIPFamilies = []k8snet.IPFamily{k8snet.IPv4, k8snet.IPv6}
			pingerMap[healthCheckIPv6] = fake.NewPinger(healthCheckIPv6)
		})

		It("should start/stop Pingers", func() {
			endpoint := newEndpointSpec(remoteClusterID1, healthCheckIP1, healthCheckIPv6)
			controller.EndpointCreatedOrUpdated(endpoint)

			pingerMap[healthCheckIP1].AwaitStart()
			pingerMap[healthCheckIPv6].AwaitStart()

			Expect(controller.Get(endpoint, k8snet.IPv4)).To(Equal(pingerMap[healthCheckIP1]))
			Expect(controller.Get(endpoint, k8snet.IPv6)).To(Equal(pingerMap[healthCheckIPv6]))

			By("Removing Endpoint")

			controller.EndpointRemoved(endpoint)

			pingerMap[healthCheckIP1].AwaitStop()
			Expect(controller.Get(endpoint, k8snet.IPv4)).To(BeNil())

			pingerMap[healthCheckIPv6].AwaitStop()
			Expect(controller.Get(endpoint, k8snet.IPv6)).To(BeNil())
		})
	})

	When("an Endpoint is updated", func() {
		var endpoint *submarinerv1.EndpointSpec

		JustBeforeEach(func() {
			endpoint = newEndpointSpec(remoteClusterID1, healthCheckIP1)
			controller.EndpointCreatedOrUpdated(endpoint)
			pingerMap[healthCheckIP1].AwaitStart()
		})

		When("the HealthCheckIP was changed", func() {
			BeforeEach(func() {
				pingerMap[healthCheckIP3] = fake.NewPinger(healthCheckIP3)
			})

			It("should stop the Pinger and start a new one", func() {
				endpoint.HealthCheckIPs = []string{healthCheckIP3}

				controller.EndpointCreatedOrUpdated(endpoint)
				pingerMap[healthCheckIP1].AwaitStop()
				pingerMap[healthCheckIP3].AwaitStart()
			})
		})

		When("the HealthCheckIP did not changed", func() {
			It("should not start a new Pinger", func() {
				controller.EndpointCreatedOrUpdated(endpoint)
				pingerMap[healthCheckIP1].AwaitNoStop()
			})
		})
	})

	When("an Endpoint has no HealthCheckIP", func() {
		It("should not start a Pinger", func() {
			controller.EndpointCreatedOrUpdated(newEndpointSpec(remoteClusterID1, ""))
			pingerMap[healthCheckIP1].AwaitNoStart()
		})
	})

	When("no supported IP families are provided", func() {
		It("should panic", func() {
			Expect(func() {
				pinger.NewController(pinger.ControllerConfig{})
			}).To(Panic())
		})
	})

	Specify("Stop should stop all pingers", func() {
		controller.EndpointCreatedOrUpdated(newEndpointSpec(remoteClusterID1, healthCheckIP1))
		pingerMap[healthCheckIP1].AwaitStart()

		controller.EndpointCreatedOrUpdated(newEndpointSpec(remoteClusterID2, healthCheckIP2))
		pingerMap[healthCheckIP2].AwaitStart()

		controller.Stop()
		pingerMap[healthCheckIP1].AwaitStop()
		pingerMap[healthCheckIP2].AwaitStop()
	})
})

func newEndpointSpec(clusterID string, healthCheckIPs ...string) *submarinerv1.EndpointSpec {
	return &submarinerv1.EndpointSpec{
		ClusterID:      clusterID,
		CableName:      fmt.Sprintf("submariner-cable-%s-192-68-1-20", clusterID),
		HealthCheckIPs: healthCheckIPs,
	}
}
