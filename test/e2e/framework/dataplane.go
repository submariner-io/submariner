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
	resourceUtil "github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/tcp"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	testAppLabel = "test-app"
)

type GlobalEgressIPType int

const (
	ClusterSelector GlobalEgressIPType = iota
	NameSpaceSelector
	PodSelector
)

func (t GlobalEgressIPType) String() string {
	switch t {
	case ClusterSelector:
		return "cluster-scoped"
	case NameSpaceSelector:
		return "namespace-scoped"
	case PodSelector:
		return "pod-scoped"
	}

	return "unknown"
}

type GlobalnetTestParams struct {
	GlobalnetEnabled bool
	GlobalEgressIP   GlobalEgressIPType
}

func VerifyDatapathConnectivity(p tcp.ConnectivityTestParams, gn GlobalnetTestParams) {
	if gn.GlobalnetEnabled {
		verifyGlobalnetDatapathConnectivity(p, gn.GlobalEgressIP)
	} else {
		tcp.RunConnectivityTest(p)
	}
}

func verifyGlobalnetDatapathConnectivity(p tcp.ConnectivityTestParams, egressIPType GlobalEgressIPType) {
	Expect(p.ToEndpointType).To(BeElementOf([]tcp.EndpointType{tcp.GlobalServiceIP, tcp.GlobalPodIP}))

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

	var service *v1.Service
	if p.ToEndpointType == tcp.GlobalServiceIP {
		service = listenerPod.CreateService()
	} else if p.ToEndpointType == tcp.GlobalPodIP {
		service = p.Framework.CreateHeadlessTCPService(listenerPod.Config.Cluster, listenerPod.Pod.Labels[testAppLabel],
			listenerPod.Config.Port)
	}

	p.Framework.CreateServiceExport(p.ToCluster, service.Name)

	remoteIP := getGlobalIngressIP(p, service)
	Expect(remoteIP).ToNot(Equal(""))

	By(fmt.Sprintf("Creating a connector pod in cluster %q, which will attempt the specific UUID handshake over TCP",
		framework.TestContext.ClusterIDs[p.FromCluster]))

	var connectorPodGlobalIPs []string

	switch egressIPType {
	case ClusterSelector:
		connectorPodGlobalIPs = p.Framework.AwaitClusterGlobalEgressIPs(p.FromCluster, constants.ClusterGlobalEgressIPName)
	case NameSpaceSelector:
		geipObject, err := newGlobalEgressIPObj(listenerPod.Pod.Namespace, nil)
		Expect(err).To(Succeed())

		err = framework.CreateGlobalEgressIP(p.FromCluster, geipObject)
		Expect(err).To(Succeed())

		connectorPodGlobalIPs = framework.AwaitGlobalEgressIPs(p.FromCluster, geipObject.GetName(), listenerPod.Pod.Namespace)
	case PodSelector:
		podSelector := &metav1.LabelSelector{MatchLabels: map[string]string{"test-app": "custom"}}
		geipObject, err := newGlobalEgressIPObj(listenerPod.Pod.Namespace, podSelector)
		Expect(err).To(Succeed())

		err = framework.CreateGlobalEgressIP(p.FromCluster, geipObject)
		Expect(err).To(Succeed())

		connectorPodGlobalIPs = framework.AwaitGlobalEgressIPs(p.FromCluster, geipObject.GetName(), listenerPod.Pod.Namespace)
	}

	Expect(connectorPodGlobalIPs).ToNot(BeEmpty())

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

	By(fmt.Sprintf("Verifying the output of the listener pod contains a %s global IP %v of the connector Pod",
		egressIPType, connectorPodGlobalIPs))

	matchIP := ContainSubstring(connectorPodGlobalIPs[0])
	for i := 1; i < len(connectorPodGlobalIPs); i++ {
		matchIP = Or(matchIP, ContainSubstring(connectorPodGlobalIPs[i]))
	}

	Expect(listenerPod.TerminationMessage).To(matchIP)

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

func getGlobalIngressIP(p tcp.ConnectivityTestParams, service *v1.Service) string {
	if p.ToEndpointType == tcp.GlobalServiceIP {
		return p.Framework.AwaitGlobalIngressIP(p.ToCluster, service.Name, service.Namespace)
	} else if p.ToEndpointType == tcp.GlobalPodIP {
		podList := p.Framework.AwaitPodsByLabelSelector(p.ToCluster, labels.Set(service.Spec.Selector).AsSelector().String(),
			service.Namespace, 1)
		ingressIPName := fmt.Sprintf("pod-%s", podList.Items[0].Name)
		return p.Framework.AwaitGlobalIngressIP(p.ToCluster, ingressIPName, service.Namespace)
	}

	return ""
}

func newGlobalEgressIPObj(namespace string, selector *metav1.LabelSelector) (*unstructured.Unstructured, error) {
	geipName := fmt.Sprintf("test-e2e-egressip-%s", namespace)
	egressIPSpec := &submarinerv1.GlobalEgressIP{
		ObjectMeta: metav1.ObjectMeta{
			Name:      geipName,
			Namespace: namespace,
		},
	}

	if selector != nil {
		egressIPSpec.Spec = submarinerv1.GlobalEgressIPSpec{
			PodSelector: selector,
		}
	}

	unstructuredEgressIPSpec, err := resourceUtil.ToUnstructured(egressIPSpec)
	if err != nil {
		return nil, err
	}

	return unstructuredEgressIPSpec, nil
}

func GetGlobalnetEgressParams(egressIP GlobalEgressIPType) GlobalnetTestParams {
	return GlobalnetTestParams{
		GlobalnetEnabled: framework.TestContext.GlobalnetEnabled,
		GlobalEgressIP:   egressIP,
	}
}
