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
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/tcp"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	subFramework "github.com/submariner-io/submariner/test/e2e/framework"
)

var _ = Describe("[external-dataplane-globalnet] Connectivity", func() {
	f := framework.NewFramework("ext-dataplane-gn")

	var toEndpointType tcp.EndpointType
	var networking framework.NetworkingType
	var cluster framework.ClusterIndex
	var egressIPType subFramework.GlobalEgressIPType

	verifyInteraction := func(clusterScheduling framework.NetworkPodScheduling, egressIPType subFramework.GlobalEgressIPType) {
		It("should be able to connect from an external app to a pod in a cluster", func() {
			if !framework.TestContext.GlobalnetEnabled {
				framework.Skipf("Globalnet is not enabled, skipping the test...")
				return
			}

			testGlobalNetExternalConnectivity(testParams{
				Framework:         f,
				ToEndpointType:    toEndpointType,
				Networking:        networking,
				Cluster:           cluster,
				ClusterScheduling: clusterScheduling,
			}, egressIPType)
		})
	}

	When("a pod on an external-app-connected cluster connects via TCP to the globalIP of a remote service", func() {
		BeforeEach(func() {
			toEndpointType = tcp.GlobalServiceIP
			networking = framework.PodNetworking
			cluster = getExternalClusterIndex(framework.TestContext.ClusterIDs)
			egressIPType = subFramework.ClusterSelector
		})

		When("the pod is not on a gateway", func() {
			// TODO: Confirm that not allowing this use case is OK.
			It("should be able to connect from an external app to a pod in a cluster", func() {
				framework.Skipf("Skipping this test for cluster directly connected to external network will be all-in-one cluster")
			})
		})

		When("the pod is on a gateway", func() {
			verifyInteraction(framework.GatewayNode, egressIPType)
		})
	})

	When("a pod on a non-external-app-connected cluster connects via TCP to the globalIP of a remote service", func() {
		BeforeEach(func() {
			toEndpointType = tcp.GlobalServiceIP
			networking = framework.PodNetworking
			cluster = getNonExternalClusterIndex(framework.TestContext.ClusterIDs)
			egressIPType = subFramework.ClusterSelector
		})

		When("the pod is not on a gateway", func() {
			verifyInteraction(framework.NonGatewayNode, egressIPType)
		})

		When("the pod is on a gateway", func() {
			verifyInteraction(framework.GatewayNode, egressIPType)
		})
	})
})

func testGlobalNetExternalConnectivity(p testParams, egressIPType subFramework.GlobalEgressIPType) {
	externalClusterName := getExternalClusterName(framework.TestContext.ClusterIDs)
	extClusterIdx := getExternalClusterIndex(framework.TestContext.ClusterIDs)

	By(fmt.Sprintf("Creating a service without selector and endpoints in cluster %q", externalClusterName))
	// Get handle for existing docker
	docker := framework.New(extAppName)
	dockerIP := docker.GetIP(extNetName)

	// Create service without selector and endpoints for dockerIP, and export the service
	extSvc := p.Framework.CreateTCPServiceWithoutSelector(extClusterIdx, "extsvc", "http", 80)
	p.Framework.CreateTCPEndpoints(extClusterIdx, extSvc.Name, "http", dockerIP, 80)
	p.Framework.CreateServiceExport(extClusterIdx, extSvc.Name)

	// Get globalIPs for the extApp to use later
	extIngressGlobalIP := p.Framework.AwaitGlobalIngressIP(extClusterIdx, extSvc.Name, extSvc.Namespace)
	Expect(extIngressGlobalIP).ToNot(Equal(""))

	extEgressGlobalIPs := p.Framework.AwaitClusterGlobalEgressIPs(extClusterIdx, constants.ClusterGlobalEgressIPName)
	Expect(extEgressGlobalIPs).ToNot(BeEmpty())

	clusterName := framework.TestContext.ClusterIDs[p.Cluster]

	By(fmt.Sprintf("Creating a pod and a service in cluster %q", clusterName))

	np := p.Framework.NewNetworkPod(&framework.NetworkPodConfig{
		Type:          framework.CustomPod,
		Port:          80,
		Cluster:       p.Cluster,
		Scheduling:    p.ClusterScheduling,
		Networking:    p.Networking,
		ContainerName: testContainerName,
		ImageName:     testImage,
		Command:       simpleHTTPServerCommand,
	})
	svc := np.CreateService()
	p.Framework.CreateServiceExport(np.Config.Cluster, svc.Name)

	// Get globalIPs for the network pod to use later
	remoteIP := p.Framework.AwaitGlobalIngressIP(np.Config.Cluster, svc.Name, svc.Namespace)
	Expect(remoteIP).ToNot(Equal(""))

	podGlobalIPs := p.Framework.AwaitClusterGlobalEgressIPs(np.Config.Cluster, constants.ClusterGlobalEgressIPName)
	Expect(podGlobalIPs).ToNot(BeEmpty())

	By(fmt.Sprintf("Sending an http request from external app %q to the service %q in the cluster %q",
		dockerIP, remoteIP, clusterName))

	command := []string{"curl", "-m", "10", fmt.Sprintf("%s:%d/%s%s", remoteIP, 80, p.Framework.Namespace, clusterName)}
	_, _ = docker.RunCommand(command...)

	By(fmt.Sprintf("Verifying the pod received the request from one of egressGlobalIPs %v", extEgressGlobalIPs))

	podLog := np.GetLog()
	if p.Cluster == extClusterIdx {
		// TODO: current behavior is that source IP from external app to the pod in the cluster that directly connected to
		// external network is the gateway IP of the pod network. Consider if it can be consistent.
		Expect(podLog).To(MatchRegexp(".*GET /%s%s .*", p.Framework.Namespace, clusterName))
	} else {
		matchRegexp := MatchRegexp("%s .*GET /%s%s .*", extEgressGlobalIPs[0], p.Framework.Namespace, clusterName)
		for i := 1; i < len(extEgressGlobalIPs); i++ {
			matchRegexp = Or(matchRegexp, MatchRegexp("%s .*GET /%s%s .*", extEgressGlobalIPs[i], p.Framework.Namespace, clusterName))
		}
		Expect(podLog).To(matchRegexp)
	}

	framework.Logf("%s", podLog)

	if p.Cluster == extClusterIdx {
		// TODO: current behavior is that access from the pod in the cluster that is directly connected to
		// external network is not reachable. Consider if it can be improved if there are use cases for it.
		return
	}

	By(fmt.Sprintf("Sending an http request from the test pod %q in cluster %q to the external app's ingressGlobalIP %q",
		np.Pod.Name, clusterName, extIngressGlobalIP))

	cmd := []string{"curl", "-m", "10", fmt.Sprintf("%s:%d/%s%s", extIngressGlobalIP, 80, p.Framework.Namespace, clusterName)}
	_, _ = np.RunCommand(cmd)

	By(fmt.Sprintf("Verifying that external app received request from one of egressGlobalIPs %v", podGlobalIPs))

	_, dockerLog := docker.GetLog()

	matchRegexp := MatchRegexp("%s .*GET /%s%s .*", podGlobalIPs[0], p.Framework.Namespace, clusterName)
	for i := 1; i < len(podGlobalIPs); i++ {
		matchRegexp = Or(matchRegexp, MatchRegexp("%s .*GET /%s%s .*", podGlobalIPs[i], p.Framework.Namespace, clusterName))
	}
	Expect(dockerLog).To(matchRegexp)

	framework.Logf("%s", dockerLog)
}
