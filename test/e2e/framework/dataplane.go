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
	"context"
	"fmt"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
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

type RunAdditionalTestFn func(listenerPodConfig framework.NetworkPodConfig, connectorPodConfig framework.NetworkPodConfig,
	service *v1.Service, verifyConnectivity func(listener *framework.NetworkPod, connector *framework.NetworkPod))

type GlobalnetTestParams struct {
	GlobalnetEnabled  bool
	GlobalEgressIP    GlobalEgressIPType
	RunAdditionalTest RunAdditionalTestFn
}

func VerifyDatapathConnectivity(p tcp.ConnectivityTestParams, gn GlobalnetTestParams) {
	if gn.GlobalnetEnabled {
		verifyGlobalnetDatapathConnectivity(p, gn)
	} else {
		tcp.RunConnectivityTest(p)
	}
}

func verifyGlobalnetDatapathConnectivity(p tcp.ConnectivityTestParams, gn GlobalnetTestParams) {
	Expect(p.ToEndpointType).To(BeElementOf([]tcp.EndpointType{tcp.GlobalServiceIP, tcp.GlobalPodIP}))

	if p.ConnectionTimeout == 0 {
		p.ConnectionTimeout = framework.TestContext.ConnectionTimeout
	}

	if p.ConnectionAttempts == 0 {
		p.ConnectionAttempts = framework.TestContext.ConnectionAttempts
	}

	var connectorPodGlobalIPs []string

	switch gn.GlobalEgressIP {
	case ClusterSelector:
		connectorPodGlobalIPs = p.Framework.AwaitClusterGlobalEgressIPs(p.FromCluster, constants.ClusterGlobalEgressIPName)
	case NameSpaceSelector:
		geipObject, err := newGlobalEgressIPObj(p.Framework.Namespace, nil)
		Expect(err).To(Succeed())

		err = framework.CreateGlobalEgressIP(p.FromCluster, geipObject)
		Expect(err).To(Succeed())

		connectorPodGlobalIPs = framework.AwaitGlobalEgressIPs(p.FromCluster, geipObject.GetName(), p.Framework.Namespace)
	case PodSelector:
		podSelector := &metav1.LabelSelector{MatchLabels: map[string]string{"test-app": "custom"}}
		geipObject, err := newGlobalEgressIPObj(p.Framework.Namespace, podSelector)
		Expect(err).To(Succeed())

		err = framework.CreateGlobalEgressIP(p.FromCluster, geipObject)
		Expect(err).To(Succeed())

		connectorPodGlobalIPs = framework.AwaitGlobalEgressIPs(p.FromCluster, geipObject.GetName(), p.Framework.Namespace)
	}

	Expect(connectorPodGlobalIPs).ToNot(BeEmpty())

	By(fmt.Sprintf("Creating a listener pod in cluster %q, which will wait for a handshake over TCP",
		framework.TestContext.ClusterIDs[p.ToCluster]))

	listenerPodConfig := &framework.NetworkPodConfig{
		Type:               framework.ListenerPod,
		Cluster:            p.ToCluster,
		Scheduling:         p.ToClusterScheduling,
		ConnectionTimeout:  p.ConnectionTimeout,
		ConnectionAttempts: p.ConnectionAttempts,
	}

	listenerPod := p.Framework.NewNetworkPod(listenerPodConfig)

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

	connectorPodConfig := &framework.NetworkPodConfig{
		Type:          framework.CustomPod,
		Cluster:       p.FromCluster,
		Scheduling:    p.FromClusterScheduling,
		Networking:    p.Networking,
		ContainerName: "connector-pod",
		ImageName:     framework.TestContext.NettestImageURL,
		Command:       []string{"sleep", "600"},
	}

	verifyConnectivity := func(listener *framework.NetworkPod, connector *framework.NetworkPod) {
		cmd := []string{"sh", "-c", "for j in $(seq 1 " + strconv.Itoa(int(connector.Config.NumOfDataBufs)) + "); do echo" +
			" [dataplane] connector says " + connector.Config.Data + "; done" +
			" | for i in $(seq " + strconv.Itoa(int(listener.Config.ConnectionAttempts)) + ");" +
			" do if nc -v " + remoteIP + " " + strconv.Itoa(connector.Config.Port) + " -w " +
			strconv.Itoa(int(listener.Config.ConnectionTimeout)) + ";" +
			" then break; else sleep " + strconv.Itoa(int(listener.Config.ConnectionTimeout/2)) + "; fi; done"}

		stdOut, _, err := p.Framework.ExecWithOptions(context.TODO(), &framework.ExecOptions{
			Command:            cmd,
			Namespace:          connector.Pod.Namespace,
			PodName:            connector.Pod.Name,
			ContainerName:      connector.Pod.Spec.Containers[0].Name,
			Stdin:              nil,
			CaptureStdout:      true,
			CaptureStderr:      true,
			PreserveWhitespace: true,
		}, connector.Config.Cluster)
		Expect(err).ToNot(HaveOccurred())

		By(fmt.Sprintf("Connector pod is scheduled on node %q", connector.Pod.Spec.NodeName))

		By(fmt.Sprintf("Waiting for the listener pod %q on node %q to exit, returning what listener sent",
			listener.Pod.Name, listener.Pod.Spec.NodeName))
		listener.AwaitFinish()
		listener.CheckSuccessfulFinish()
		p.Framework.DeletePod(p.FromCluster, connector.Pod.Name, connector.Pod.Namespace)

		By("Verifying that the listener got the connector's data and the connector got the listener's data")
		Expect(listener.TerminationMessage).To(ContainSubstring(connector.Config.Data))
		Expect(stdOut).To(ContainSubstring(listener.Config.Data))

		By(fmt.Sprintf("Verifying the output of the listener pod contains a %s global IP %v of the connector Pod",
			gn.GlobalEgressIP, connectorPodGlobalIPs))

		matchIP := ContainSubstring(connectorPodGlobalIPs[0])
		for i := 1; i < len(connectorPodGlobalIPs); i++ {
			matchIP = Or(matchIP, ContainSubstring(connectorPodGlobalIPs[i]))
		}

		Expect(listener.TerminationMessage).To(matchIP)

		p.Framework.DeletePod(listener.Config.Cluster, listener.Pod.Name, listener.Pod.Namespace)
	}

	verifyConnectivity(listenerPod, p.Framework.NewNetworkPod(connectorPodConfig))

	if gn.RunAdditionalTest != nil {
		gn.RunAdditionalTest(*listenerPodConfig, *connectorPodConfig, service, verifyConnectivity)
	}

	p.Framework.DeleteServiceExport(p.ToCluster, service.Name)
	p.Framework.DeleteService(p.ToCluster, service.Name)
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

func CanExecuteNonGatewayConnectivityTest(sourceNode, destNode framework.NetworkPodScheduling,
	sourceCluster, destCluster framework.ClusterIndex,
) bool {
	if sourceNode == framework.NonGatewayNode &&
		framework.TestContext.NumNodesInCluster[sourceCluster] == 1 {
		framework.Skipf("Skipping the test as cluster %q has only a single node...",
			framework.TestContext.ClusterIDs[sourceCluster])
		return false
	}

	if destNode == framework.NonGatewayNode &&
		framework.TestContext.NumNodesInCluster[destCluster] == 1 {
		framework.Skipf("Skipping the test as cluster %q has only a single node...",
			framework.TestContext.ClusterIDs[destCluster])
		return false
	}

	return true
}
