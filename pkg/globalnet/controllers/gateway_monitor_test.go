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
	fake "github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	testutil "github.com/submariner-io/admiral/pkg/test"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	fakesubmariner "github.com/submariner-io/submariner/pkg/client/clientset/versioned/fake"
	"github.com/submariner-io/submariner/pkg/globalnet/constants"
	"github.com/submariner-io/submariner/pkg/globalnet/controllers"
	netlinkAPI "github.com/submariner-io/submariner/pkg/netlink"
	fakeNetlink "github.com/submariner-io/submariner/pkg/netlink/fake"
	"github.com/submariner-io/submariner/pkg/packetfilter"
	routeAgent "github.com/submariner-io/submariner/pkg/routeagent_driver/constants"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	fakek8s "k8s.io/client-go/kubernetes/fake"
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
			t.createPFilterChain(packetfilter.TableTypeNAT, kubeProxyIPTableChainName)
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
				t.awaitGlobalnetChainsCleared()
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

					t.awaitGlobalnetChainsCleared()
				})
			})
		})

		Context("and a stale internal service is being deleted", func() {
			const serviceName = "stale-service"
			internalServiceName := controllers.GetInternalSvcName(serviceName)

			JustBeforeEach(func() {
				internalService := &corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name: internalServiceName,
						Labels: map[string]string{
							controllers.InternalServiceLabel: serviceName,
						},
						Finalizers: []string{controllers.InternalServiceFinalizer},
					},
				}

				_, err := t.services.Create(context.Background(), resource.MustToUnstructured(internalService), metav1.CreateOptions{})
				Expect(err).To(Succeed())

				Expect(t.services.Delete(context.Background(), internalServiceName, metav1.DeleteOptions{})).To(Succeed())
			})

			It("should remove the finalizer", func() {
				test.AwaitNoResource(t.services, internalServiceName)
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
			t.pFilter.AwaitRule(packetfilter.TableTypeNAT, constants.SmGlobalnetMarkChain, ContainSubstring(remoteCIDR))

			Expect(t.endpoints.Delete(context.TODO(), endpoint.Name, metav1.DeleteOptions{})).To(Succeed())
			t.pFilter.AwaitNoRule(packetfilter.TableTypeNAT, constants.SmGlobalnetMarkChain, ContainSubstring(remoteCIDR))
		})
	})

	When("a remote Endpoint with an overlapping CIDR is created", func() {
		It("should not add expected IP table rule(s)", func() {
			t.createEndpoint(newEndpointSpec(remoteClusterID, t.hostName, localCIDR))
			time.Sleep(500 * time.Millisecond)
			t.pFilter.AwaitNoRule(packetfilter.TableTypeNAT, constants.SmGlobalnetMarkChain, ContainSubstring(localCIDR))
		})
	})
})

var _ = Describe("Uninstall", func() {
	var t *testDriverBase

	const ipSetName = controllers.IPSetPrefix + "abd"

	BeforeEach(func() {
		t = newTestDriverBase()
		t.initChains()

		Expect(t.pFilter.NewNamedSet(&packetfilter.SetInfo{
			Name: ipSetName,
		}).Create(true)).To(Succeed())

		Expect(t.pFilter.NewNamedSet(&packetfilter.SetInfo{
			Name: "other",
		}).Create(true)).To(Succeed())
	})

	Specify("UninstallDataPath should remove the IP table chains and sets", func() {
		controllers.UninstallDataPath()

		for _, chain := range []string{
			constants.SmGlobalnetEgressChainForCluster,
			constants.SmGlobalnetEgressChainForHeadlessSvcPods,
			constants.SmGlobalnetEgressChainForHeadlessSvcEPs,
			constants.SmGlobalnetEgressChainForNamespace,
			constants.SmGlobalnetEgressChainForPods,
			constants.SmGlobalnetIngressChain,
			constants.SmGlobalnetMarkChain,
			constants.SmGlobalnetEgressChain,
		} {
			t.pFilter.AwaitNoChain(packetfilter.TableTypeNAT, chain)
		}

		t.pFilter.AwaitSetDeleted(ipSetName)
		t.pFilter.AwaitSet("other")
	})

	Specify("RemoveGlobalIPAnnotationOnNode should remove the annotation if it exists", func() {
		os.Setenv("NODE_NAME", nodeName)

		kubeClient := fakek8s.NewSimpleClientset()

		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
		}

		// No annotation present should be a no-op.
		_, err := kubeClient.CoreV1().Nodes().Create(context.Background(), node, metav1.CreateOptions{})
		Expect(err).To(Succeed())

		controllers.RemoveGlobalIPAnnotationOnNode(kubeClient)

		Expect(kubeClient.CoreV1().Nodes().Delete(context.Background(), nodeName, metav1.DeleteOptions{})).To(Succeed())

		node.Annotations = map[string]string{constants.SmGlobalIP: "1.2.3.4"}

		_, err = kubeClient.CoreV1().Nodes().Create(context.Background(), node, metav1.CreateOptions{})
		Expect(err).To(Succeed())

		controllers.RemoveGlobalIPAnnotationOnNode(kubeClient)

		node, err = kubeClient.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		Expect(err).To(Succeed())
		Expect(node.Annotations).ToNot(HaveKey(constants.SmGlobalIP))
	})

	Specify("DeleteGlobalnetObjects should delete all Globalnet resources and internal Services", func() {
		submClient := fakesubmariner.NewSimpleClientset()
		fake.AddBasicReactors(&submClient.Fake)

		clusterEgressIP, err := submClient.SubmarinerV1().ClusterGlobalEgressIPs("").Create(context.Background(),
			&submarinerv1.ClusterGlobalEgressIP{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster-egressIP",
				},
			}, metav1.CreateOptions{})
		Expect(err).To(Succeed())

		egressIP, err := submClient.SubmarinerV1().GlobalEgressIPs(namespace).Create(context.Background(),
			&submarinerv1.GlobalEgressIP{
				ObjectMeta: metav1.ObjectMeta{
					Name: "egressIP",
				},
			}, metav1.CreateOptions{})
		Expect(err).To(Succeed())

		serviceName := "service"
		internalServiceName := controllers.GetInternalSvcName(serviceName)

		internalService := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name: internalServiceName,
				Labels: map[string]string{
					controllers.InternalServiceLabel: serviceName,
				},
				Finalizers: []string{controllers.InternalServiceFinalizer},
			},
		}

		_, err = t.services.Create(context.Background(), resource.MustToUnstructured(internalService), metav1.CreateOptions{})
		Expect(err).To(Succeed())

		ingressIP, err := submClient.SubmarinerV1().GlobalIngressIPs(namespace).Create(context.Background(),
			&submarinerv1.GlobalIngressIP{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ingressIP",
				},
				Spec: submarinerv1.GlobalIngressIPSpec{
					ServiceRef: &corev1.LocalObjectReference{Name: serviceName},
				},
			}, metav1.CreateOptions{})
		Expect(err).To(Succeed())

		_, err = submClient.SubmarinerV1().GlobalIngressIPs(namespace).Create(context.Background(),
			&submarinerv1.GlobalIngressIP{
				ObjectMeta: metav1.ObjectMeta{
					Name: "ingressIP-no-service-ref",
				},
			}, metav1.CreateOptions{})
		Expect(err).To(Succeed())

		controllers.DeleteGlobalnetObjects(submClient, t.dynClient)

		_, err = submClient.SubmarinerV1().ClusterGlobalEgressIPs("").Get(context.Background(), clusterEgressIP.Name,
			metav1.GetOptions{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		_, err = submClient.SubmarinerV1().GlobalEgressIPs(namespace).Get(context.Background(), egressIP.Name,
			metav1.GetOptions{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		_, err = submClient.SubmarinerV1().GlobalIngressIPs(namespace).Get(context.Background(), ingressIP.Name,
			metav1.GetOptions{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		test.AwaitNoResource(t.services, internalServiceName)
	})
})

type gatewayMonitorTestDriver struct {
	*testDriverBase
	endpoints            dynamic.ResourceInterface
	hostName             string
	kubeClient           *fakek8s.Clientset
	leaderElectionConfig controllers.LeaderElectionConfig
	leaderElection       *testutil.LeaderElectionSupport
}

func newGatewayMonitorTestDriver() *gatewayMonitorTestDriver {
	t := &gatewayMonitorTestDriver{}

	BeforeEach(func() {
		t.testDriverBase = newTestDriverBase()

		t.endpoints = t.dynClient.Resource(*test.GetGroupVersionResourceFor(t.restMapper, &submarinerv1.Endpoint{})).
			Namespace(namespace)

		t.kubeClient = fakek8s.NewSimpleClientset()
		t.leaderElectionConfig = controllers.LeaderElectionConfig{}
		t.leaderElection = testutil.NewLeaderElectionSupport(t.kubeClient, namespace, controllers.LeaderElectionLockName)

		os.Setenv("SUBMARINER_NAMESPACE", namespace)
		os.Setenv("SUBMARINER_CLUSTERID", clusterID)

		netlinkAPI.NewFunc = func() netlinkAPI.Interface {
			return fakeNetlink.New()
		}
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
		RestMapper: t.restMapper,
		Client:     t.dynClient,
		Scheme:     t.scheme,
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

	t.pFilter.AwaitChain(packetfilter.TableTypeNAT, constants.SmGlobalnetMarkChain)
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
	t.pFilter.AwaitChain(packetfilter.TableTypeNAT, constants.SmGlobalnetIngressChain)
	t.pFilter.AwaitChain(packetfilter.TableTypeNAT, constants.SmGlobalnetEgressChain)
	t.pFilter.AwaitChain(packetfilter.TableTypeNAT, constants.SmGlobalnetEgressChainForPods)
	t.pFilter.AwaitChain(packetfilter.TableTypeNAT, constants.SmGlobalnetEgressChainForHeadlessSvcPods)
	t.pFilter.AwaitChain(packetfilter.TableTypeNAT, constants.SmGlobalnetEgressChainForHeadlessSvcEPs)
	t.pFilter.AwaitChain(packetfilter.TableTypeNAT, constants.SmGlobalnetEgressChainForNamespace)
	t.pFilter.AwaitChain(packetfilter.TableTypeNAT, constants.SmGlobalnetEgressChainForCluster)
	t.pFilter.AwaitChain(packetfilter.TableTypeNAT, routeAgent.SmPostRoutingChain)
	t.pFilter.AwaitChain(packetfilter.TableTypeNAT, constants.SmGlobalnetMarkChain)
}

func (t *gatewayMonitorTestDriver) awaitGlobalnetChainsCleared() {
	t.pFilter.AwaitNoRules(packetfilter.TableTypeNAT, constants.SmGlobalnetIngressChain)
	t.pFilter.AwaitNoRules(packetfilter.TableTypeNAT, constants.SmGlobalnetEgressChain)
	t.pFilter.AwaitNoRules(packetfilter.TableTypeNAT, constants.SmGlobalnetMarkChain)
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
