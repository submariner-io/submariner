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
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
)

const (
	extAppName = "ext-app"
	extNetName = "pseudo-ext"

	testImage         = "registry.access.redhat.com/ubi7/ubi:latest"
	testContainerName = "ext-test-container"
)

var (
	simpleHTTPServerCommand = []string{"python", "-m", "SimpleHTTPServer", "80"}
)

var _ = Describe("[external-dataplane] Connectivity", func() {
	f := framework.NewFramework("ext-dataplane")

	It("should be able to connect from an external app to a pod in a cluster", func() {
		if framework.TestContext.GlobalnetEnabled {
			testGlobalNetExternalConnectivity(f)
		} else {
			testExternalConnectivity(f)
		}
	})
})

func testExternalConnectivity(f *framework.Framework) {
	externalClusterName := getExternalClusterName(framework.TestContext.ClusterIDs)

	for idx := range framework.KubeClients {
		clusterName := framework.TestContext.ClusterIDs[idx]

		By(fmt.Sprintf("Creating a pod and a service in cluster %q", clusterName))

		np := f.NewNetworkPod(&framework.NetworkPodConfig{
			Type:          framework.CustomPod,
			Port:          80,
			Cluster:       framework.ClusterIndex(idx),
			Scheduling:    framework.NonGatewayNode,
			ContainerName: testContainerName,
			ImageName:     testImage,
			Command:       simpleHTTPServerCommand,
		})
		svc := np.CreateService()

		// Get handle for existing docker.
		docker := framework.New(extAppName)

		// Get IPs to use later.
		podIP := np.Pod.Status.PodIP
		svcIP := svc.Spec.ClusterIP
		dockerIP := docker.GetIP(extNetName)

		By(fmt.Sprintf("Sending an http request from external app %q to the service %q in the cluster %q",
			dockerIP, svcIP, clusterName))

		command := []string{"curl", "-m", "10", fmt.Sprintf("%s:%d/%s%s", svcIP, 80, f.Namespace, clusterName)}
		_, _ = docker.RunCommand(command...)

		By("Verifying the pod received the request")

		podLog := np.GetLog()

		if clusterName == externalClusterName {
			Expect(podLog).To(MatchRegexp(".*GET /%s%s .*", f.Namespace, clusterName))
		} else {
			Expect(podLog).To(MatchRegexp("%s .*GET /%s%s .*", dockerIP, f.Namespace, clusterName))
		}

		By(fmt.Sprintf("Sending an http request from the test pod %q %q in cluster %q to the external app %q",
			np.Pod.Name, podIP, clusterName, dockerIP))

		cmd := []string{"curl", "-m", "10", fmt.Sprintf("%s:%d/%s%s", dockerIP, 80, f.Namespace, clusterName)}
		_, _ = np.RunCommand(cmd)

		By("Verifying that external app received request")

		// Only check stderr.
		_, dockerLog := docker.GetLog()

		if clusterName == externalClusterName {
			Expect(dockerLog).To(MatchRegexp(".*GET /%s%s .*", f.Namespace, clusterName))
		} else {
			Expect(dockerLog).To(MatchRegexp("%s .*GET /%s%s .*", podIP, f.Namespace, clusterName))
		}
	}
}

func testGlobalNetExternalConnectivity(f *framework.Framework) {
	externalClusterName := getExternalClusterName(framework.TestContext.ClusterIDs)
	extClusterIdx := getExternalClusterIndex(framework.TestContext.ClusterIDs)

	By(fmt.Sprintf("Creating a service without selector and endpoints in cluster %q", externalClusterName))

	// Get handle for existing docker.
	docker := framework.New(extAppName)
	dockerIP := docker.GetIP(extNetName)

	// Create service without selector and endpoints for dockerIP, and export the service.
	extSvc := f.CreateTCPServiceWithoutSelector(extClusterIdx, "extsvc", "http", 80)
	f.CreateTCPEndpoints(extClusterIdx, extSvc.Name, "http", dockerIP, 80)
	f.CreateServiceExport(extClusterIdx, extSvc.Name)

	// Get globalIPs for the extApp to use later.
	extIngressGlobalIP := f.AwaitGlobalIngressIP(extClusterIdx, extSvc.Name, extSvc.Namespace)
	Expect(extIngressGlobalIP).ToNot(Equal(""))

	extEgressGlobalIPs := f.AwaitClusterGlobalEgressIPs(extClusterIdx, constants.ClusterGlobalEgressIPName)
	Expect(extEgressGlobalIPs).ToNot(BeEmpty())

	for idx := range framework.KubeClients {
		clusterName := framework.TestContext.ClusterIDs[idx]

		By(fmt.Sprintf("Creating a pod and a service in cluster %q", clusterName))

		np := f.NewNetworkPod(&framework.NetworkPodConfig{
			Type:    framework.CustomPod,
			Port:    80,
			Cluster: framework.ClusterIndex(idx),
			// Also test NonGatewayNode
			Scheduling:    framework.GatewayNode,
			ContainerName: testContainerName,
			ImageName:     testImage,
			Command:       simpleHTTPServerCommand,
		})
		svc := np.CreateService()
		f.CreateServiceExport(np.Config.Cluster, svc.Name)

		// Get globalIPs for the network pod to use later.
		remoteIP := f.AwaitGlobalIngressIP(np.Config.Cluster, svc.Name, svc.Namespace)
		Expect(remoteIP).ToNot(Equal(""))

		podGlobalIPs := f.AwaitClusterGlobalEgressIPs(np.Config.Cluster, constants.ClusterGlobalEgressIPName)
		Expect(podGlobalIPs).ToNot(BeEmpty())

		By(fmt.Sprintf("Sending an http request from external app %q to the service %q in the cluster %q",
			dockerIP, remoteIP, clusterName))

		command := []string{"curl", "-m", "10", fmt.Sprintf("%s:%d/%s%s", remoteIP, 80, f.Namespace, clusterName)}
		_, _ = docker.RunCommand(command...)

		By(fmt.Sprintf("Verifying the pod received the request from one of egressGlobalIPs %v", extEgressGlobalIPs))

		podLog := np.GetLog()
		if framework.ClusterIndex(idx) == extClusterIdx {
			// TODO: current behavior is that source IP from external app to the pod in the cluster that directly connected to
			// external network is the gateway IP of the pod network. Consider if it can be consistent.
			Expect(podLog).To(MatchRegexp(".*GET /%s%s .*", f.Namespace, clusterName))
		} else {
			matchRegexp := MatchRegexp("%s .*GET /%s%s .*", extEgressGlobalIPs[0], f.Namespace, clusterName)
			for i := 1; i < len(extEgressGlobalIPs); i++ {
				matchRegexp = Or(matchRegexp, MatchRegexp("%s .*GET /%s%s .*", extEgressGlobalIPs[i], f.Namespace, clusterName))
			}
			Expect(podLog).To(matchRegexp)
		}

		framework.Logf("%s", podLog)

		By(fmt.Sprintf("Sending an http request from the test pod %q in cluster %q to the external app's ingressGlobalIP %q",
			np.Pod.Name, clusterName, extIngressGlobalIP))

		cmd := []string{"curl", "-m", "10", fmt.Sprintf("%s:%d/%s%s", extIngressGlobalIP, 80, f.Namespace, clusterName)}
		_, _ = np.RunCommand(cmd)

		By(fmt.Sprintf("Verifying that external app received request from one of podGlobalIPs %v", podGlobalIPs))

		_, dockerLog := docker.GetLog()

		if framework.ClusterIndex(idx) == extClusterIdx {
			// TODO: current behavior is that source IP from the pod in the cluster that directly connected to
			// external network to external pod is not egressGlobalIP. Consider if it can be consistent.
			Expect(dockerLog).To(MatchRegexp(".*GET /%s%s .*", f.Namespace, clusterName))
		} else {
			matchRegexp := MatchRegexp("%s .*GET /%s%s .*", podGlobalIPs[0], f.Namespace, clusterName)
			for i := 1; i < len(podGlobalIPs); i++ {
				matchRegexp = Or(matchRegexp, MatchRegexp("%s .*GET /%s%s .*", podGlobalIPs[i], f.Namespace, clusterName))
			}
			Expect(dockerLog).To(matchRegexp)
		}

		framework.Logf("%s", dockerLog)
	}
}

// The first cluster is chosen as the one connected to external application.
// See scripts/e2e/external/utils.
func getExternalClusterName(names []string) string {
	if len(names) == 0 {
		return ""
	}

	sortedNames := make([]string, len(names))
	copy(sortedNames, names)
	sort.Strings(sortedNames)

	return sortedNames[0]
}

func getExternalClusterIndex(names []string) framework.ClusterIndex {
	clusterName := getExternalClusterName(names)

	for idx, cid := range names {
		if cid == clusterName {
			return framework.ClusterIndex(idx)
		}
	}

	// TODO: consider right error handling.
	return framework.ClusterIndex(0)
}
