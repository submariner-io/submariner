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
	resourceUtil "github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/shipyard/test/e2e/framework"
	"github.com/submariner-io/shipyard/test/e2e/tcp"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	subFramework "github.com/submariner-io/submariner/test/e2e/framework"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
)

type globalnetTestParams struct {
	ClusterEgressIPType subFramework.GlobalEgressIPType
}

/*
   - Test environment for external network w/ globalnet is as follows:

                  [ext-app]  [gateway-cluster]  [non-gateway-cluster]
                     |            |       |        |
    pseudo-ext *-----+------------+--*    |        |
                                          |        |
    kind                            *-----+--------+-------*


   - For globalnet environment, expected behaviors of connectivity and source IPs are:

       from / to       |    ext-app    |  gateway-cluster  |  non-gateway-cluster  |
   ------------------- | ------------- | ----------------- | --------------------- |
   ext-app             |    N/A        |       R(*2)(*4)   |        S(*3)          |
   gateway-cluster     |    R(*2)(*4)  |       N/A         |        S(*1)          |
   non-gateway-cluster |    S(*3)      |       S(*1)       |        N/A            |

   Legend: N: Not reachable, R: Reachable (source IP isn't globalIP),
           S: Source IP is global IP (and reachable)

   (*1) Not covered in this test, but covered in normal connectivity tests.
   (*2) Pod w/ hostnetwork isn't reachable.
   (*3) Pod w/ hostnetwork isn't reachable when pod isn't on a gateway node.
   (*4) Pod isn't reachable, when pod isn't on a gateway node

   Note that the current expected use cases are:
   (a) From ext-app to non-gateway-cluster
   (b) From non-gateway-cluster to ext-app
*/

var _ = Describe("[external-dataplane-globalnet] Connectivity", func() {
	f := framework.NewFramework("ext-dataplane-gn")

	var toEndpointType tcp.EndpointType
	var networking framework.NetworkingType
	var cluster framework.ClusterIndex
	var egressIPType subFramework.GlobalEgressIPType
	var err error

	verifyInteraction := func(clusterScheduling framework.NetworkPodScheduling) {
		It("should be able to connect from/to an external app to/from a pod in a cluster", func() {
			if !framework.TestContext.GlobalnetEnabled {
				framework.Skipf("Globalnet is not enabled, skipping the test...")
				return
			}

			testGlobalNetExternalConnectivity(
				testParams{
					Framework:         f,
					ToEndpointType:    toEndpointType,
					Networking:        networking,
					Cluster:           cluster,
					ClusterScheduling: clusterScheduling,
				},
				globalnetTestParams{
					ClusterEgressIPType: egressIPType,
				})
		})
	}

	When("connected from a gateway-cluster", func() {
		BeforeEach(func() {
			cluster, err = getGatewayClusterIndex(framework.TestContext.ClusterIDs)
			Expect(err).NotTo(HaveOccurred())
		})

		When("a pod connects via TCP to/from the globalIP of a remote service", func() {
			BeforeEach(func() {
				toEndpointType = tcp.GlobalServiceIP
				networking = framework.PodNetworking
				egressIPType = subFramework.ClusterSelector
			})

			// Access from a pod on NonGatewayNodes to external apps is not supported for a gateway-cluster (*4)
			PWhen("the pod is not on a gateway", func() {
				verifyInteraction(framework.NonGatewayNode)
			})

			When("the pod is on a gateway", func() {
				verifyInteraction(framework.GatewayNode)
			})
		})

		When("a pod matching an egress IP namespace selector connects via TCP to/from the globalIP of a remote service", func() {
			BeforeEach(func() {
				toEndpointType = tcp.GlobalServiceIP
				networking = framework.PodNetworking
				egressIPType = subFramework.NameSpaceSelector
			})

			// Access from a pod on NonGatewayNodes to external apps is not supported for a gateway-cluster (*4)
			PWhen("the pod is not on a gateway", func() {
				verifyInteraction(framework.NonGatewayNode)
			})

			When("the pod is on a gateway", func() {
				verifyInteraction(framework.GatewayNode)
			})
		})

		When("a pod matching an egress IP pod selector connects via TCP to/from the globalIP of a remote service", func() {
			BeforeEach(func() {
				toEndpointType = tcp.GlobalServiceIP
				networking = framework.PodNetworking
				egressIPType = subFramework.PodSelector
			})

			// Access from a pod on NonGatewayNodes to external apps is not supported for a gateway-cluster (*4)
			PWhen("the pod is not on a gateway", func() {
				verifyInteraction(framework.NonGatewayNode)
			})

			When("the pod is on a gateway", func() {
				verifyInteraction(framework.GatewayNode)
			})
		})

		// Access from a HostNetworking pod to external apps is not supported (*2)
		PWhen("a pod with HostNetworking connects via TCP to/from the globalIP of a remote service", func() {
		})

		When("a pod connects via TCP to the globalIP of a remote headless service", func() {
			BeforeEach(func() {
				toEndpointType = tcp.GlobalPodIP
				networking = framework.PodNetworking
				egressIPType = subFramework.ClusterSelector
			})

			// Access from a pod on NonGatewayNodes to external apps is not supported for a gateway-cluster (*4)
			PWhen("the pod is not on a gateway", func() {
				verifyInteraction(framework.NonGatewayNode)
			})

			When("the pod is on a gateway", func() {
				verifyInteraction(framework.GatewayNode)
			})
		})
	})

	When("connected from a non-gateway-cluster", func() {
		BeforeEach(func() {
			cluster, err = getNonGatewayClusterIndex(framework.TestContext.ClusterIDs)
			Expect(err).NotTo(HaveOccurred())
		})

		When("a pod connects via TCP to/from the globalIP of a remote service", func() {
			BeforeEach(func() {
				toEndpointType = tcp.GlobalServiceIP
				networking = framework.PodNetworking
				egressIPType = subFramework.ClusterSelector
			})

			When("the pod is not on a gateway", func() {
				verifyInteraction(framework.NonGatewayNode)
			})

			When("the pod is on a gateway", func() {
				verifyInteraction(framework.GatewayNode)
			})
		})

		When("a pod matching an egress IP namespace selector connects via TCP to/from the globalIP of a remote service", func() {
			BeforeEach(func() {
				toEndpointType = tcp.GlobalServiceIP
				networking = framework.PodNetworking
				egressIPType = subFramework.NameSpaceSelector
			})

			When("the pod is not on a gateway", func() {
				verifyInteraction(framework.NonGatewayNode)
			})

			When("the pod is on a gateway", func() {
				verifyInteraction(framework.GatewayNode)
			})
		})

		When("a pod matching an egress IP pod selector connects via TCP to/from the globalIP of a remote service", func() {
			BeforeEach(func() {
				toEndpointType = tcp.GlobalServiceIP
				networking = framework.PodNetworking
				egressIPType = subFramework.PodSelector
			})

			When("the pod is not on a gateway", func() {
				verifyInteraction(framework.NonGatewayNode)
			})

			When("the pod is on a gateway", func() {
				verifyInteraction(framework.GatewayNode)
			})
		})

		When("a pod with HostNetworking connects via TCP to/from the globalIP of a remote service", func() {
			BeforeEach(func() {
				toEndpointType = tcp.GlobalServiceIP
				networking = framework.HostNetworking
				egressIPType = subFramework.ClusterSelector
			})

			// Access from a pod with HostNetworking fails if a pod is not on NonGateway nodes (*3)
			// TODO: it needs to be documented?
			PWhen("the pod is not on a gateway", func() {
				verifyInteraction(framework.NonGatewayNode)
			})

			When("the pod is on a gateway", func() {
				verifyInteraction(framework.GatewayNode)
			})
		})

		When("a pod connects via TCP to/from the globalIP of a remote headless service", func() {
			BeforeEach(func() {
				toEndpointType = tcp.GlobalPodIP
				networking = framework.PodNetworking
				egressIPType = subFramework.ClusterSelector
			})

			When("the pod is not on a gateway", func() {
				verifyInteraction(framework.NonGatewayNode)
			})

			When("the pod is on a gateway", func() {
				verifyInteraction(framework.GatewayNode)
			})
		})
	})
})

func testGlobalNetExternalConnectivity(p testParams, g globalnetTestParams) {
	gatewayCluster := getGatewayClusterName(framework.TestContext.ClusterIDs)
	extClusterIdx, err := getGatewayClusterIndex(framework.TestContext.ClusterIDs)
	Expect(err).NotTo(HaveOccurred())

	By(fmt.Sprintf("Creating a service without selector and endpoints in cluster %q", gatewayCluster))
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
	svc := createSvc(p, np)
	p.Framework.CreateServiceExport(np.Config.Cluster, svc.Name)

	// Get globalIPs for the network pod to use later
	remoteIP := getGlobalIngressIP(p, svc)
	Expect(remoteIP).ToNot(Equal(""))

	podGlobalIPs := getPodGlobalIPs(p, g, np)
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
	_, dockerLog := docker.GetLog()

	switch p.ToEndpointType {
	default:
		fallthrough
	case tcp.PodIP, tcp.ServiceIP:
		framework.Failf("Unsupported ToEndpointType %v was passed", p.ToEndpointType)
	case tcp.GlobalServiceIP:
		By(fmt.Sprintf("Verifying that external app received request from one of podGlobalIPs %v", podGlobalIPs))
		matchRegexp := MatchRegexp("%s .*GET /%s%s .*", podGlobalIPs[0], p.Framework.Namespace, clusterName)

		for i := 1; i < len(podGlobalIPs); i++ {
			matchRegexp = Or(matchRegexp, MatchRegexp("%s .*GET /%s%s .*", podGlobalIPs[i], p.Framework.Namespace, clusterName))
		}
		Expect(dockerLog).To(matchRegexp)
	case tcp.GlobalPodIP:
		// For access from headless service, source IP is the globalIngressIP of the pod, which is set to remoteIP
		By(fmt.Sprintf("Verifying that external app received request from globalIngressIP of the pod %v", remoteIP))
		Expect(dockerLog).To(MatchRegexp("%s .*GET /%s%s .*", remoteIP, p.Framework.Namespace, clusterName))
	}

	framework.Logf("%s", dockerLog)
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

func createSvc(p testParams, np *framework.NetworkPod) *v1.Service {
	switch p.ToEndpointType {
	case tcp.GlobalServiceIP, tcp.PodIP, tcp.ServiceIP:
		return np.CreateService()
	case tcp.GlobalPodIP:
		return p.Framework.CreateHeadlessTCPService(np.Config.Cluster, np.Pod.Labels["test-app"],
			np.Config.Port)
	default:
		framework.Failf("Unsupported ToEndpointType %v was passed", p.ToEndpointType)
	}

	return nil
}

func getGlobalIngressIP(p testParams, service *v1.Service) string {
	switch p.ToEndpointType {
	default:
		fallthrough
	case tcp.PodIP, tcp.ServiceIP:
		framework.Failf("Unsupported ToEndpointType %v was passed", p.ToEndpointType)
	case tcp.GlobalServiceIP:
		return p.Framework.AwaitGlobalIngressIP(p.Cluster, service.Name, service.Namespace)
	case tcp.GlobalPodIP:
		podList := p.Framework.AwaitPodsByLabelSelector(p.Cluster, labels.Set(service.Spec.Selector).AsSelector().String(),
			service.Namespace, 1)
		ingressIPName := fmt.Sprintf("pod-%s", podList.Items[0].Name)

		return p.Framework.AwaitGlobalIngressIP(p.Cluster, ingressIPName, service.Namespace)
	}

	return ""
}

func getPodGlobalIPs(p testParams, g globalnetTestParams, np *framework.NetworkPod) []string {
	switch g.ClusterEgressIPType {
	case subFramework.ClusterSelector:
		return p.Framework.AwaitClusterGlobalEgressIPs(p.Cluster, constants.ClusterGlobalEgressIPName)
	case subFramework.NameSpaceSelector:
		geipObject, err := newGlobalEgressIPObj(np.Pod.Namespace, nil)
		Expect(err).To(Succeed())

		err = framework.CreateGlobalEgressIP(p.Cluster, geipObject)
		Expect(err).To(Succeed())

		return framework.AwaitGlobalEgressIPs(p.Cluster, geipObject.GetName(), np.Pod.Namespace)
	case subFramework.PodSelector:
		podSelector := &metav1.LabelSelector{MatchLabels: map[string]string{"test-app": "custom"}}
		geipObject, err := newGlobalEgressIPObj(np.Pod.Namespace, podSelector)
		Expect(err).To(Succeed())

		err = framework.CreateGlobalEgressIP(p.Cluster, geipObject)
		Expect(err).To(Succeed())

		return framework.AwaitGlobalEgressIPs(p.Cluster, geipObject.GetName(), np.Pod.Namespace)
	}

	return []string{}
}
