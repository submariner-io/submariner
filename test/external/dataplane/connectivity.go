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
	"sort"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/tcp"
)

const (
	extAppName = "ext-app"
	extNetName = "pseudo-ext"

	testImage         = "registry.access.redhat.com/ubi7/ubi:latest"
	testContainerName = "ext-test-container"
)

var simpleHTTPServerCommand = []string{"python", "-m", "SimpleHTTPServer", "80"}

type testParams struct {
	Framework         *framework.Framework
	ToEndpointType    tcp.EndpointType
	Networking        framework.NetworkingType
	Cluster           framework.ClusterIndex
	ClusterScheduling framework.NetworkPodScheduling
}

/*
   - Test environment for external network w/o globalnet is as follows:

                  [ext-app]  [gateway-cluster]  [non-gateway-cluster]
                     |            |       |        |
    pseudo-ext *-----+------------+--*    |        |
                                          |        |
    kind                            *-----+--------+-------*


   - For non-globalnet environment, expected behaviors of connectivity and source IPs are:

       from / to       |  ext-app  |  gateway-cluster  |  non-gateway-cluster  |
   ------------------- | --------- | ----------------- | --------------------- |
   ext-app             | N/A       |       R(*2)(*3)   |        R(*2)          |
   gateway-cluster     | R(*2)     |       N/A         |        R(*1)          |
   non-gateway-cluster | R(*2)(*3) |       R(*1)       |        N/A            |

   Legend: N: Not reachable, R: Reachable (source IP isn't globalIP),
           S: Source IP is global IP (and reachable)

   (*1) Not covered in this test, but covered in normal connectivity tests.
   (*2) Pod w/ hostnetwork isn't reachable.
   (*3) Pod isn't reachable, when pod isn't on a gateway node

   Note that the current expected use cases are:
   (a) From ext-app to non-gateway-cluster
   (b) From non-gateway-cluster to ext-app
*/

var _ = Describe("[external-dataplane] Connectivity", func() {
	f := framework.NewFramework("ext-dataplane")

	var toEndpointType tcp.EndpointType
	var networking framework.NetworkingType
	var cluster framework.ClusterIndex
	var err error

	verifyInteraction := func(clusterScheduling framework.NetworkPodScheduling) {
		It("should be able to connect from/to an external app to/from a pod in a cluster", func() {
			if framework.TestContext.GlobalnetEnabled {
				framework.Skipf("Globalnet is enabled, skipping the test...")
				return
			}

			testExternalConnectivity(testParams{
				Framework:         f,
				ToEndpointType:    toEndpointType,
				Networking:        networking,
				Cluster:           cluster,
				ClusterScheduling: clusterScheduling,
			})
		})
	}

	When("connected from a gateway-cluster", func() {
		BeforeEach(func() {
			cluster, err = getGatewayClusterIndex(framework.TestContext.ClusterIDs)
			Expect(err).NotTo(HaveOccurred())
		})

		When("a pod connects via TCP to/from a remote pod", func() {
			BeforeEach(func() {
				toEndpointType = tcp.PodIP
				networking = framework.PodNetworking
			})

			// Access from a pod on NonGatewayNodes to external apps is not supported for a gateway-cluster (*3)
			PWhen("the pod is not on a gateway", func() {
				verifyInteraction(framework.NonGatewayNode)
			})

			When("the pod is on a gateway-node", func() {
				verifyInteraction(framework.GatewayNode)
			})
		})

		When("a pod connects via TCP to/from a remote service", func() {
			BeforeEach(func() {
				toEndpointType = tcp.ServiceIP
				networking = framework.PodNetworking
			})

			// Access from a pod on NonGatewayNodes to external apps is not supported for a gateway-cluster (*3)
			PWhen("the pod is not on a gateway", func() {
				verifyInteraction(framework.NonGatewayNode)
			})

			When("the pod is on a gateway-node", func() {
				verifyInteraction(framework.GatewayNode)
			})
		})

		// Access from a hostnetwork pod to external apps is not supported (*2)
		PWhen("a pod with HostNetworking connects via TCP to/from a remote service", func() {
		})
	})

	When("connected from a non-gateway-cluster", func() {
		BeforeEach(func() {
			cluster, err = getNonGatewayClusterIndex(framework.TestContext.ClusterIDs)
			Expect(err).NotTo(HaveOccurred())
		})

		When("a pod connects via TCP to/from a remote pod", func() {
			BeforeEach(func() {
				toEndpointType = tcp.PodIP
				networking = framework.PodNetworking
			})

			When("the pod is not on a gateway", func() {
				verifyInteraction(framework.NonGatewayNode)
			})

			When("the pod is on a gateway", func() {
				verifyInteraction(framework.GatewayNode)
			})
		})

		When("a pod connects via TCP to/from a remote service", func() {
			BeforeEach(func() {
				toEndpointType = tcp.ServiceIP
				networking = framework.PodNetworking
			})

			When("the pod is not on a gateway", func() {
				verifyInteraction(framework.NonGatewayNode)
			})

			When("the pod is on a gateway", func() {
				verifyInteraction(framework.GatewayNode)
			})
		})

		// Access from a hostnetwork pod to external apps is not supported (*2)
		PWhen("a pod with HostNetworking connects via TCP to/from a remote service", func() {
		})
	})
})

func testExternalConnectivity(p testParams) {
	gatewayCluster := getGatewayClusterName(framework.TestContext.ClusterIDs)

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

	// Get handle for existing docker
	docker := framework.New(extAppName)

	// Get IPs to use later
	podIP := np.Pod.Status.PodIP
	svcIP := svc.Spec.ClusterIP
	dockerIP := docker.GetIP(extNetName)

	var targetIP string

	switch p.ToEndpointType {
	default:
		fallthrough
	case tcp.GlobalPodIP, tcp.GlobalServiceIP:
		framework.Failf("Unsupported ToEndpointType %v was passed", p.ToEndpointType)
	case tcp.PodIP:
		targetIP = podIP
	case tcp.ServiceIP:
		targetIP = svcIP
	}

	By(fmt.Sprintf("Sending an http request from external app %q to %q in the cluster %q",
		dockerIP, targetIP, clusterName))

	command := []string{"curl", "-m", "10", fmt.Sprintf("%s:%d/%s%s", targetIP, 80, p.Framework.Namespace, clusterName)}
	_, _ = docker.RunCommand(command...)

	By("Verifying the pod received the request")

	podLog := np.GetLog()

	if clusterName == gatewayCluster {
		Expect(podLog).To(MatchRegexp(".*GET /%s%s .*", p.Framework.Namespace, clusterName))
	} else {
		Expect(podLog).To(MatchRegexp("%s .*GET /%s%s .*", dockerIP, p.Framework.Namespace, clusterName))
	}

	By(fmt.Sprintf("Sending an http request from the test pod %q %q in cluster %q to the external app %q",
		np.Pod.Name, podIP, clusterName, dockerIP))

	cmd := []string{"curl", "-m", "10", fmt.Sprintf("%s:%d/%s%s", dockerIP, 80, p.Framework.Namespace, clusterName)}
	_, _ = np.RunCommand(cmd)

	By("Verifying that external app received request")
	// Only check stderr
	_, dockerLog := docker.GetLog()

	if clusterName == gatewayCluster {
		Expect(dockerLog).To(MatchRegexp(".*GET /%s%s .*", p.Framework.Namespace, clusterName))
	} else {
		Expect(dockerLog).To(MatchRegexp("%s .*GET /%s%s .*", podIP, p.Framework.Namespace, clusterName))
	}
}

// The first cluster is chosen as the one connected to external application.
// See scripts/e2e/external/utils.
func getGatewayClusterName(names []string) string {
	if len(names) == 0 {
		return ""
	}

	sortedNames := make([]string, len(names))
	copy(sortedNames, names)
	sort.Strings(sortedNames)

	return sortedNames[0]
}

func getGatewayClusterIndex(names []string) (framework.ClusterIndex, error) {
	clusterName := getGatewayClusterName(names)

	for idx, cid := range names {
		if cid == clusterName {
			return framework.ClusterIndex(idx), nil
		}
	}

	return framework.ClusterIndex(0),
		fmt.Errorf("failed to find gateway-cluster (gateway-cluster name: %s, cluster names: %v)", clusterName, names)
}

func getNonGatewayClusterIndex(names []string) (framework.ClusterIndex, error) {
	clusterName := getGatewayClusterName(names)

	for idx, cid := range names {
		if cid != clusterName {
			return framework.ClusterIndex(idx), nil
		}
	}

	return framework.ClusterIndex(0),
		fmt.Errorf("failed to find non-gateway-cluster (gateway-cluster name: %s, cluster names: %v)", clusterName, names)
}
