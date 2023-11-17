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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	testutil "github.com/submariner-io/admiral/pkg/test"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
	routeAgent "github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

const (
	clusterID       = "east"
	remoteClusterID = "west"
	remoteCIDR      = "169.254.2.0/24"
	nodeName        = "raiders"
)

var _ = Describe("Endpoint monitoring", func() {
	t := newGatewayMonitorTestDriver()

	var endpoint *submarinerv1.Endpoint

	When("a local gateway Endpoint corresponding to the controller host is created", func() {
		JustBeforeEach(func() {
			t.createNode(nodeName, "", "")
			endpoint = t.createEndpoint(newEndpointSpec(clusterID, t.hostName, localCIDR))
			t.createIPTableChain("nat", kubeProxyIPTableChainName)
		})

		It("should start the controllers", func() {
			t.awaitControllersStarted()

			t.createGlobalEgressIP(newGlobalEgressIP(globalEgressIPName, nil, nil))
			t.awaitGlobalEgressIPStatusAllocated(globalEgressIPName, 1)

			t.createServiceExport(t.createService(newClusterIPService()))
			t.awaitIngressIPStatusAllocated(serviceName)

			service := newClusterIPService()
			service.Name = "headless-nginx"
			service = toHeadlessService(service)
			backendPod := newHeadlessServicePod(service.Name)
			t.createPod(backendPod)
			t.createServiceExport(t.createService(service))
			t.awaitHeadlessGlobalIngressIP(service.Name, backendPod.Name)
		})

		Context("and then deleted and recreated", func() {
			JustBeforeEach(func() {
				t.awaitControllersStarted()

				By("Deleting the Endpoint")

				Expect(t.endpoints.Delete(context.TODO(), endpoint.Name, metav1.DeleteOptions{})).To(Succeed())
			})

			It("should stop and restart the controllers", func() {
				t.leaderElection.AwaitLeaseReleased()
				t.awaitNoGlobalnetChains()
				t.ensureControllersStopped()

				By("Recreating the Endpoint")

				time.Sleep(time.Millisecond * 300)
				t.createEndpoint(newEndpointSpec(clusterID, t.hostName, localCIDR))

				t.leaderElection.AwaitLeaseAcquired()
				t.awaitGlobalnetChains()

				t.awaitIngressIPStatusAllocated(serviceName)
			})
		})

		Context("and then updated", func() {
			BeforeEach(func() {
				t.leaderElectionConfig.LeaseDuration = time.Hour * 3
				t.leaderElectionConfig.RenewDeadline = time.Hour * 2
				t.leaderElectionConfig.RetryPeriod = time.Hour
			})

			JustBeforeEach(func() {
				t.awaitControllersStarted()

				// Since the RenewDeadline and RetryPeriod are set very high and the leader lock has been acquired, leader election should
				// not try to renew the leader lock at this point, but we'll wait a bit more just in case to give it plenty of time. After
				// that and after we update the Endpoint below, any updates to the leader lock means it tried to re-acquire it.
				time.Sleep(time.Millisecond * 500)
				t.kubeClient.ClearActions()

				By("Updating the Endpoint")

				endpoint.Annotations = map[string]string{"foo": "bar"}
				test.UpdateResource(t.endpoints, endpoint)
			})

			It("should not try to re-acquire the leader lock", func() {
				testutil.EnsureNoActionsForResource(&t.kubeClient.Fake, "leases", "update")
			})
		})

		Context("and then a local gateway Endpoint corresponding to another host is created", func() {
			JustBeforeEach(func() {
				t.awaitControllersStarted()

				By("Creating other Endpoint")

				t.createEndpoint(newEndpointSpec(clusterID, t.hostName+"-other", localCIDR))
			})

			It("should stop the controllers", func() {
				t.leaderElection.AwaitLeaseReleased()
				t.awaitNoGlobalnetChains()
				t.ensureControllersStopped()
			})
		})

		Context("and then renewal of the leader lock fails", func() {
			BeforeEach(func() {
				t.leaderElectionConfig.RenewDeadline = time.Millisecond * 200
				t.leaderElectionConfig.RetryPeriod = time.Millisecond * 20
			})

			JustBeforeEach(func() {
				t.awaitControllersStarted()

				By("Setting leases resource updates to fail")

				t.leaderElection.FailLease(t.leaderElectionConfig.RenewDeadline)

				By("Ensuring controllers are stopped and globalnet chains are not cleared")

				t.ensureControllersStopped()
				t.awaitGlobalnetChains()
			})

			It("should re-acquire the leader lock after the failure is cleared", func() {
				By("Setting leases resource updates to succeed")

				t.leaderElection.SucceedLease()

				By("Ensuring lease was renewed")

				t.leaderElection.AwaitLeaseRenewed()

				t.awaitIngressIPStatusAllocated(serviceName)
			})

			Context("and then the gateway Endpoint is deleted", func() {
				It("should clear the globalnet chains", func() {
					By("Deleting the Endpoint")

					Expect(t.endpoints.Delete(context.TODO(), endpoint.Name, metav1.DeleteOptions{})).To(Succeed())

					t.awaitNoGlobalnetChains()
				})
			})
		})
	})

	Context("and a local gateway Endpoint corresponding to another host is created", func() {
		JustBeforeEach(func() {
			endpoint = t.createEndpoint(newEndpointSpec(clusterID, t.hostName+"-other", localCIDR))
		})

		It("should not start the controllers", func() {
			t.leaderElection.EnsureLeaseNotAcquired()

			t.createServiceExport(t.createService(newClusterIPService()))
			t.ensureNoGlobalIngressIP(serviceName)
		})
	})

	When("a remote Endpoint with non-overlapping CIDRs is created then removed", func() {
		It("should add/remove appropriate IP table rule(s)", func() {
			endpoint := t.createEndpoint(newEndpointSpec(remoteClusterID, t.hostName, remoteCIDR))
			t.ipt.AwaitRule("nat", constants.SmGlobalnetMarkChain, ContainSubstring(remoteCIDR))

			Expect(t.endpoints.Delete(context.TODO(), endpoint.Name, metav1.DeleteOptions{})).To(Succeed())
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
	endpoints            dynamic.ResourceInterface
	hostName             string
	kubeClient           *k8sfake.Clientset
	leaderElectionConfig controllers.LeaderElectionConfig
	leaderElection       *testutil.LeaderElectionSupport
}

func newGatewayMonitorTestDriver() *gatewayMonitorTestDriver {
	t := &gatewayMonitorTestDriver{}

	BeforeEach(func() {
		t.testDriverBase = newTestDriverBase()

		t.endpoints = t.dynClient.Resource(*test.GetGroupVersionResourceFor(t.restMapper, &submarinerv1.Endpoint{})).
			Namespace(namespace)

		t.kubeClient = k8sfake.NewSimpleClientset()
		t.leaderElectionConfig = controllers.LeaderElectionConfig{}
		t.leaderElection = testutil.NewLeaderElectionSupport(t.kubeClient, namespace, controllers.LeaderElectionLockName)
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

	t.controller, err = controllers.NewGatewayMonitor(&controllers.GatewayMonitorConfig{
		Config: watcher.Config{
			RestMapper: t.restMapper,
			Client:     t.dynClient,
			Scheme:     t.scheme,
		},
		Spec: controllers.Specification{
			ClusterID:  clusterID,
			Namespace:  namespace,
			GlobalCIDR: []string{localCIDR},
		},
		LocalCIDRs:           localSubnets,
		KubeClient:           t.kubeClient,
		LeaderElectionConfig: t.leaderElectionConfig,
		Hostname:             t.hostName,
	})

	Expect(err).To(Succeed())
	Expect(t.controller.Start()).To(Succeed())

	t.ipt.AwaitChain("nat", constants.SmGlobalnetMarkChain)
}

func (t *gatewayMonitorTestDriver) createEndpoint(spec *submarinerv1.EndpointSpec) *submarinerv1.Endpoint {
	endpointName, err := spec.GenerateName()
	Expect(err).To(Succeed())

	endpoint := &submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: endpointName,
		},
		Spec: *spec,
	}

	return test.CreateResource(t.endpoints, endpoint)
}

func (t *gatewayMonitorTestDriver) awaitControllersStarted() {
	t.leaderElection.AwaitLeaseAcquired()
	t.awaitGlobalnetChains()
	t.awaitClusterGlobalEgressIPStatusAllocated(controllers.DefaultNumberOfClusterEgressIPs)
}

func (t *gatewayMonitorTestDriver) ensureControllersStopped() {
	time.Sleep(300 * time.Millisecond)
	t.createServiceExport(t.createService(newClusterIPService()))
	t.ensureNoGlobalIngressIP(serviceName)
}

func (t *gatewayMonitorTestDriver) awaitGlobalnetChains() {
	t.ipt.AwaitChain("nat", constants.SmGlobalnetIngressChain)
	t.ipt.AwaitChain("nat", constants.SmGlobalnetEgressChain)
	t.ipt.AwaitChain("nat", routeAgent.SmPostRoutingChain)
	t.ipt.AwaitChain("nat", constants.SmGlobalnetMarkChain)
}

func (t *gatewayMonitorTestDriver) awaitNoGlobalnetChains() {
	t.ipt.AwaitNoChain("nat", constants.SmGlobalnetIngressChain)
	t.ipt.AwaitNoChain("nat", constants.SmGlobalnetEgressChain)
	t.ipt.AwaitNoChain("nat", constants.SmGlobalnetMarkChain)
}

func newEndpointSpec(clusterID, hostname, subnet string) *submarinerv1.EndpointSpec {
	return &submarinerv1.EndpointSpec{
		CableName: fmt.Sprintf("submariner-cable-%s-%s", clusterID, hostname),
		ClusterID: clusterID,
		PrivateIP: "192.68.1.2",
		Hostname:  hostname,
		Subnets:   []string{subnet},
	}
}
