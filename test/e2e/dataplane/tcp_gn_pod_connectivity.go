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
	. "github.com/onsi/ginkgo/v2"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/tcp"
	subFramework "github.com/submariner-io/submariner/test/e2e/framework"
)

var _ = Describe("[dataplane-globalnet] Basic TCP connectivity tests across overlapping clusters without discovery", func() {
	f := framework.NewFramework("dataplane-gn-conn-nd")
	var toEndpointType tcp.EndpointType
	var networking framework.NetworkingType
	var egressIPType subFramework.GlobalEgressIPType
	var fromCluster framework.ClusterIndex
	var toCluster framework.ClusterIndex

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
			}, subFramework.GetGlobalnetEgressParams(egressIPType))
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

		When("the pod is not on a gateway and the remote service is not on a gateway", func() {
			verifyInteraction(framework.NonGatewayNode, framework.NonGatewayNode)
		})

		When("the pod is on a gateway and the remote service is not on a gateway", func() {
			verifyInteraction(framework.GatewayNode, framework.NonGatewayNode)
		})

		When("the pod is on a gateway and the remote service is on a gateway", func() {
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
