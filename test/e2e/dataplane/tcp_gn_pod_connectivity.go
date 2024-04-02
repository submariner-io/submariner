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

package dataplane

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/util"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/tcp"
	subFramework "github.com/submariner-io/submariner/test/e2e/framework"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var _ = Describe("Basic TCP connectivity tests across overlapping clusters without discovery", Label(TestLabel, "globalnet"), func() {
	f := framework.NewFramework("dataplane-gn-conn-nd")

	var (
		toEndpointType    tcp.EndpointType
		networking        framework.NetworkingType
		egressIPType      subFramework.GlobalEgressIPType
		fromCluster       framework.ClusterIndex
		toCluster         framework.ClusterIndex
		runAdditionalTest subFramework.RunAdditionalTestFn
	)

	BeforeEach(func() {
		runAdditionalTest = nil
	})

	verifyInteraction := func(fromClusterScheduling, toClusterScheduling framework.NetworkPodScheduling) {
		It("should have sent the expected data from the pod to the other pod", func() {
			if !framework.TestContext.GlobalnetEnabled {
				framework.Skipf("Globalnet is not enabled, skipping the test...")
				return
			}

			if !subFramework.CanExecuteNonGatewayConnectivityTest(fromClusterScheduling, toClusterScheduling,
				framework.ClusterA, framework.ClusterB) {
				return
			}

			subFramework.VerifyDatapathConnectivity(tcp.ConnectivityTestParams{
				Framework:             f,
				ToEndpointType:        toEndpointType,
				Networking:            networking,
				FromCluster:           fromCluster,
				FromClusterScheduling: fromClusterScheduling,
				ToCluster:             toCluster,
				ToClusterScheduling:   toClusterScheduling,
			}, subFramework.GlobalnetTestParams{
				GlobalnetEnabled:  framework.TestContext.GlobalnetEnabled,
				GlobalEgressIP:    egressIPType,
				RunAdditionalTest: runAdditionalTest,
			})
		})
	}

	When("a pod connects via TCP to the globalIP of a remote service", func() {
		BeforeEach(func() {
			toEndpointType = tcp.GlobalServiceIP
			networking = framework.PodNetworking
			egressIPType = subFramework.ClusterSelector
			fromCluster = framework.ClusterA
			toCluster = framework.ClusterB
		})

		When("the pod is not on a gateway and the remote service is not on a gateway", Label(framework.BasicTestLabel), func() {
			BeforeEach(func() {
				runAdditionalTest = func(lpConfig framework.NetworkPodConfig, cpConfig framework.NetworkPodConfig, service *v1.Service,
					verifyConnectivity func(listener *framework.NetworkPod, connector *framework.NetworkPod),
				) {
					lpConfig.Port++
					cpConfig.Port = lpConfig.Port

					framework.By(fmt.Sprintf("Updating service port to %d", lpConfig.Port))

					err := util.Update(context.Background(), resource.ForService(framework.KubeClients[lpConfig.Cluster],
						f.Namespace), service, func(existing *v1.Service) (*v1.Service, error) {
						existing.Spec.Ports[0].Port = int32(lpConfig.Port)
						existing.Spec.Ports[0].TargetPort = intstr.FromInt32(int32(lpConfig.Port))
						return existing, nil
					})
					Expect(err).To(Succeed())

					verifyConnectivity(f.NewNetworkPod(&lpConfig), f.NewNetworkPod(&cpConfig))
				}
			})

			verifyInteraction(framework.NonGatewayNode, framework.NonGatewayNode)
		})

		When("the pod is on a gateway and the remote service is not on a gateway", func() {
			verifyInteraction(framework.GatewayNode, framework.NonGatewayNode)
		})

		When("the pod is on a gateway and the remote service is on a gateway", Label(framework.BasicTestLabel), func() {
			verifyInteraction(framework.GatewayNode, framework.GatewayNode)
		})
	})

	When("a pod matching an egress IP namespace selector connects via TCP to the globalIP of a remote service", func() {
		BeforeEach(func() {
			toEndpointType = tcp.GlobalServiceIP
			networking = framework.PodNetworking
			egressIPType = subFramework.NameSpaceSelector
			fromCluster = framework.ClusterA
			toCluster = framework.ClusterB
		})

		When("the pod is not on a gateway and the remote service is not on a gateway", func() {
			verifyInteraction(framework.NonGatewayNode, framework.NonGatewayNode)
		})

		When("the pod is on a gateway and the remote service is on a gateway", func() {
			verifyInteraction(framework.GatewayNode, framework.GatewayNode)
		})
	})

	When("a pod matching an egress IP pod selector connects via TCP to the globalIP of a remote service", func() {
		BeforeEach(func() {
			toEndpointType = tcp.GlobalServiceIP
			networking = framework.PodNetworking
			egressIPType = subFramework.PodSelector
			fromCluster = framework.ClusterA
			toCluster = framework.ClusterB
		})

		When("the pod is not on a gateway and the remote service is not on a gateway", func() {
			verifyInteraction(framework.NonGatewayNode, framework.NonGatewayNode)
		})

		When("the pod is on a gateway and the remote service is on a gateway", func() {
			verifyInteraction(framework.GatewayNode, framework.GatewayNode)
		})
	})

	When("a pod with HostNetworking connects via TCP to the globalIP of a remote service", func() {
		BeforeEach(func() {
			toEndpointType = tcp.GlobalServiceIP
			networking = framework.HostNetworking
			egressIPType = subFramework.ClusterSelector
			fromCluster = framework.ClusterA
			toCluster = framework.ClusterB
		})

		When("the pod is not on a gateway and the remote service is not on a gateway", func() {
			verifyInteraction(framework.NonGatewayNode, framework.NonGatewayNode)
		})

		When("the pod is on a gateway and the remote service is not on a gateway", func() {
			verifyInteraction(framework.GatewayNode, framework.NonGatewayNode)
		})
	})

	When("a pod connects via TCP to the globalIP of a remote headless service", func() {
		BeforeEach(func() {
			toEndpointType = tcp.GlobalPodIP
			networking = framework.PodNetworking
			egressIPType = subFramework.ClusterSelector
			fromCluster = framework.ClusterA
			toCluster = framework.ClusterB
		})

		When("the pod is not on a gateway and the remote service is not on a gateway", func() {
			verifyInteraction(framework.NonGatewayNode, framework.NonGatewayNode)
		})

		When("the pod is on a gateway and the remote service is on a gateway", func() {
			verifyInteraction(framework.GatewayNode, framework.GatewayNode)
		})
	})

	When("a pod connects via TCP to the globalIP of a remote service in reverse direction", func() {
		BeforeEach(func() {
			toEndpointType = tcp.GlobalServiceIP
			networking = framework.PodNetworking
			egressIPType = subFramework.ClusterSelector
			fromCluster = framework.ClusterB
			toCluster = framework.ClusterA
		})

		When("the pod is not on a gateway and the remote service is not on a gateway", func() {
			verifyInteraction(framework.NonGatewayNode, framework.NonGatewayNode)
		})
	})
})
