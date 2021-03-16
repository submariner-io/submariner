/*
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
		testExternalConnectivity(f)
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

		// Get handle for existing docker
		docker := framework.New(extAppName)

		// Get IPs to use later
		podIP := np.Pod.Status.PodIP
		svcIP := svc.Spec.ClusterIP
		dockerIP := docker.GetIP(extNetName)

		By(fmt.Sprintf("Sending an http request from external app %q to the service %q in the cluster %q",
			dockerIP, svcIP, clusterName))

		command := []string{"curl", "-m", "10", fmt.Sprintf("%s:%d", svcIP, 80)}
		_, _ = docker.RunCommand(command...)

		By("Verifying the pod received the request")

		podLog := np.GetLog()

		// TODO: also verify cluster that is directly connected to external app
		// (source IP is not dockerIP in the case, so need to be checked by the other way).
		if clusterName != externalClusterName {
			Expect(podLog).To(ContainSubstring(dockerIP))
		}

		By(fmt.Sprintf("Sending an http request from the test pod %q %q in cluster %q to the external app %q",
			np.Pod.Name, podIP, clusterName, dockerIP))

		cmd := []string{"curl", "-m", "10", fmt.Sprintf("%s:%d", dockerIP, 80)}
		_, _ = np.RunCommand(cmd)

		By("Verifying that external app received request")
		// Only check stderr
		_, dockerLog := docker.GetLog()

		// TODO: also verify cluster that is directly connected to external app
		// (source IP is not podIP in the case, so need to be checked by the other way).
		if clusterName != externalClusterName {
			Expect(dockerLog).To(ContainSubstring(podIP))
		}
	}
}

// The first cluster is chosen as the one connected to external application
// See scripts/e2e/external/utils
func getExternalClusterName(names []string) string {
	if len(names) == 0 {
		return ""
	}

	sortedNames := make([]string, len(names))
	copy(sortedNames, names)
	sort.Strings(sortedNames)

	return sortedNames[0]
}
