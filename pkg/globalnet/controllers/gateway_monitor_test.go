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
package controllers_test

import (
	"context"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
	"github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	"github.com/submariner-io/submariner/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
)

const (
	clusterID       = "east"
	remoteClusterID = "west"
	remoteCIDR      = "169.254.2.0/24"
	nodeName        = "raiders"
)

var _ = Describe("Endpoint monitoring", func() {
	t := newGatewayMonitorTestDriver()

	var endpointName string

	When("a local Endpoint is created", func() {
		JustBeforeEach(func() {
			endpointName = t.createEndpoint(newEndpointSpec(clusterID, t.hostName, localCIDR))
			t.createIPTableChain("nat", kubeProxyIPTableChainName)
		})

		It("should add the appropriate IP table chains", func() {
			t.ipt.AwaitChain("nat", constants.SmGlobalnetIngressChain)
			t.ipt.AwaitChain("nat", constants.SmGlobalnetEgressChain)
			t.ipt.AwaitChain("nat", constants.SmPostRoutingChain)
			t.ipt.AwaitChain("nat", constants.SmGlobalnetMarkChain)
		})

		It("should start the controllers", func() {
			t.awaitClusterGlobalEgressIPStatusAllocated(1)

			t.createGlobalEgressIP(newGlobalEgressIP(globalEgressIPName, nil, nil))
			t.awaitGlobalEgressIPStatusAllocated(globalEgressIPName, 1)

			t.createServiceExport(t.createService(newClusterIPService()))
			t.awaitIngressIPStatusAllocated(serviceName)
		})

		Context("and then removed", func() {
			JustBeforeEach(func() {
				t.awaitClusterGlobalEgressIPStatusAllocated(1)

				Expect(t.endpoints.Delete(context.TODO(), endpointName, metav1.DeleteOptions{})).To(Succeed())
			})

			It("should remove the appropriate IP table chains", func() {
				t.ipt.AwaitNoChain("nat", constants.SmGlobalnetIngressChain)
				t.ipt.AwaitNoChain("nat", constants.SmGlobalnetEgressChain)
				t.ipt.AwaitNoChain("nat", constants.SmGlobalnetMarkChain)
			})

			It("should stop the controllers", func() {
				t.ipt.AwaitNoChain("nat", constants.SmGlobalnetMarkChain)

				time.Sleep(300 * time.Millisecond)
				t.createGlobalEgressIP(newGlobalEgressIP(globalEgressIPName, nil, nil))
				awaitNoAllocatedIPs(t.globalEgressIPs, globalEgressIPName)

				t.createServiceExport(t.createService(newClusterIPService()))
				t.awaitNoGlobalIngressIP(serviceName)
			})
		})
	})

	When("a remote Endpoint with non-overlapping CIDRs is created then removed", func() {
		It("should add/remove appropriate IP table rule(s)", func() {
			endpointName := t.createEndpoint(newEndpointSpec(remoteClusterID, t.hostName, remoteCIDR))
			t.ipt.AwaitRule("nat", constants.SmGlobalnetMarkChain, ContainSubstring(remoteCIDR))

			Expect(t.endpoints.Delete(context.TODO(), endpointName, metav1.DeleteOptions{})).To(Succeed())
			t.ipt.AwaitNoRule("nat", constants.SmGlobalnetMarkChain, ContainSubstring(remoteCIDR))
		})
	})

	When("a remote Endpoint with an overlapping CIDR is created", func() {
		It("should not add expected IP table rule(s)", func() {
			t.createEndpoint(newEndpointSpec(remoteClusterID, t.hostName, localCIDR))
			time.Sleep(500 * time.Millisecond)
			t.ipt.AwaitNoRule("nat", constants.SmGlobalnetMarkChain, ContainSubstring(localCIDR))
		})
	})
})

type gatewayMonitorTestDriver struct {
	*testDriverBase
	endpoints dynamic.ResourceInterface
	hostName  string
}

func newGatewayMonitorTestDriver() *gatewayMonitorTestDriver {
	t := &gatewayMonitorTestDriver{}

	BeforeEach(func() {
		t.testDriverBase = newTestDriverBase()

		t.endpoints = t.dynClient.Resource(*test.GetGroupVersionResourceFor(t.restMapper, &submarinerv1.Endpoint{})).
			Namespace(namespace)
	})

	JustBeforeEach(func() {
		t.start()
	})

	AfterEach(func() {
		t.testDriverBase.afterEach()
	})

	return t
}

func (t *gatewayMonitorTestDriver) start() {
	os.Setenv("NODE_NAME", nodeName)
	var err error

	localSubnets := []string{}
	t.hostName, err = os.Hostname()
	Expect(err).To(Succeed())

	t.controller, err = controllers.NewGatewayMonitor(controllers.Specification{
		ClusterID:  clusterID,
		Namespace:  namespace,
		GlobalCIDR: []string{localCIDR},
	}, localSubnets, watcher.Config{
		RestMapper: t.restMapper,
		Client:     t.dynClient,
		Scheme:     t.scheme,
	})

	Expect(err).To(Succeed())
	Expect(t.controller.Start()).To(Succeed())

	t.ipt.AwaitChain("nat", constants.SmGlobalnetMarkChain)
}

func (t *gatewayMonitorTestDriver) createEndpoint(spec *submarinerv1.EndpointSpec) string {
	endpointName, err := util.GetEndpointCRDNameFromParams(spec.ClusterID, spec.CableName)
	Expect(err).To(Succeed())

	test.CreateResource(t.endpoints, &submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: endpointName,
		},
		Spec: *spec,
	})

	return endpointName
}

func newEndpointSpec(clusterID, hostname, subnet string) *submarinerv1.EndpointSpec {
	return &submarinerv1.EndpointSpec{
		CableName: fmt.Sprintf("submariner-cable-%s-192-68-1-2", clusterID),
		ClusterID: clusterID,
		PrivateIP: "192-68-1-2",
		Hostname:  hostname,
		Subnets:   []string{subnet},
	}
}
