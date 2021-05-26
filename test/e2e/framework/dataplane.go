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
package framework

import (
	"fmt"
	"strconv"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/submariner-io/shipyard/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/tcp"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	v1 "k8s.io/api/core/v1"
)

func VerifyDatapathConnectivity(p tcp.ConnectivityTestParams, globalnetEnabled bool) {
	if globalnetEnabled {
		verifyGlobalnetDatapathConnectivity(p)
	} else {
		tcp.RunConnectivityTest(p)
	}
}

func verifyGlobalnetDatapathConnectivity(p tcp.ConnectivityTestParams) {
	if p.ConnectionTimeout == 0 {
		p.ConnectionTimeout = framework.TestContext.ConnectionTimeout
	}

	if p.ConnectionAttempts == 0 {
		p.ConnectionAttempts = framework.TestContext.ConnectionAttempts
	}

	By(fmt.Sprintf("Creating a listener pod in cluster %q, which will wait for a handshake over TCP",
		framework.TestContext.ClusterIDs[p.ToCluster]))

	listenerPod := p.Framework.NewNetworkPod(&framework.NetworkPodConfig{
		Type:               framework.ListenerPod,
		Cluster:            p.ToCluster,
		Scheduling:         p.ToClusterScheduling,
		ConnectionTimeout:  p.ConnectionTimeout,
		ConnectionAttempts: p.ConnectionAttempts,
	})

	By(fmt.Sprintf("Pointing a ClusterIP service to the listener pod in cluster %q",
		framework.TestContext.ClusterIDs[p.ToCluster]))

	service := listenerPod.CreateService()
	p.Framework.CreateServiceExport(p.ToCluster, service.Name)

	// Wait for the globalIP annotation on the service.
	service = p.Framework.AwaitUntilAnnotationOnService(p.ToCluster, constants.SmGlobalIP, service.Name, service.Namespace)
	remoteIP := service.GetAnnotations()[constants.SmGlobalIP]

	By(fmt.Sprintf("Creating a connector pod in cluster %q, which will attempt the specific UUID handshake over TCP",
		framework.TestContext.ClusterIDs[p.FromCluster]))

	connectorPod := p.Framework.NewNetworkPod(&framework.NetworkPodConfig{
		Type:          framework.CustomPod,
		Cluster:       p.FromCluster,
		Scheduling:    p.FromClusterScheduling,
		Networking:    p.Networking,
		ContainerName: "connector-pod",
		ImageName:     "quay.io/submariner/nettest:devel",
		Command:       []string{"sleep", "600"},
	})

	cmd := []string{"sh", "-c", "for j in $(seq 50); do echo [dataplane] connector says " + connectorPod.Config.Data + "; done" +
		" | for i in $(seq " + strconv.Itoa(int(p.ConnectionAttempts)) + ");" +
		" do if nc -v " + remoteIP + " " + strconv.Itoa(connectorPod.Config.Port) + " -w " + strconv.Itoa(int(p.ConnectionTimeout)) + ";" +
		" then break; else sleep " + strconv.Itoa(int(p.ConnectionTimeout/2)) + "; fi; done"}

	stdOut, _, err := execCmdInBash(p, cmd, connectorPod.Pod)
	Expect(err).To(BeNil())

	By(fmt.Sprintf("Waiting for the listener pod %q on node %q to exit, returning what listener sent",
		listenerPod.Pod.Name, listenerPod.Pod.Spec.NodeName))
	listenerPod.AwaitFinish()
	listenerPod.CheckSuccessfulFinish()
	p.Framework.DeletePod(p.FromCluster, connectorPod.Pod.Name, connectorPod.Pod.Namespace)

	By("Verifying that the listener got the connector's data and the connector got the listener's data")
	Expect(listenerPod.TerminationMessage).To(ContainSubstring(connectorPod.Config.Data))
	Expect(stdOut).To(ContainSubstring(listenerPod.Config.Data))

	if p.Networking == framework.PodNetworking && p.ToEndpointType == tcp.GlobalIP {
		// When Globalnet is enabled (i.e., remoteEndpoint is a globalIP) and POD uses PodNetworking,
		// Globalnet Controller MASQUERADEs the source-ip of the POD to the corresponding global-ip
		// that is assigned to the POD.
		By("Verifying the output of listener pod which must contain the globalIP of the connector POD")

		podGlobalIP := connectorPod.Pod.GetAnnotations()[constants.SmGlobalIP]

		Expect(podGlobalIP).ToNot(Equal(""))
		Expect(listenerPod.TerminationMessage).To(ContainSubstring(podGlobalIP))
	} else if p.Networking == framework.HostNetworking {
		// when a POD is using the HostNetwork, it does not get an IPAddress from the podCIDR
		// but it uses the HostIP. Submariner, for such PODs, would MASQUERADE the sourceIP of
		// the outbound traffic (destined to remoteCluster) to the corresponding CNI interface
		// ip-address on that Host and globalIP will NOT be annotated on the POD.
		By("Verifying that globalIP annotation does not exist on the connector POD")
		podGlobalIP := connectorPod.Pod.GetAnnotations()[constants.SmGlobalIP]
		Expect(podGlobalIP).To(Equal(""))
	}

	p.Framework.DeleteService(p.ToCluster, service.Name)
	p.Framework.DeleteServiceExport(p.ToCluster, service.Name)
	p.Framework.DeletePod(p.ToCluster, listenerPod.Pod.Name, listenerPod.Pod.Namespace)
	p.Framework.DeletePod(p.FromCluster, connectorPod.Pod.Name, connectorPod.Pod.Namespace)
}

func execCmdInBash(p tcp.ConnectivityTestParams, cmd []string, pod *v1.Pod) (string, string, error) {
	execOptions := framework.ExecOptions{
		Command:            cmd,
		Namespace:          pod.Namespace,
		PodName:            pod.Name,
		ContainerName:      pod.Spec.Containers[0].Name,
		Stdin:              nil,
		CaptureStdout:      true,
		CaptureStderr:      true,
		PreserveWhitespace: true,
	}

	return p.Framework.ExecWithOptions(execOptions, p.FromCluster)
}
