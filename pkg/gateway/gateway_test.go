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

package gateway_test

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/certificate"
	fakecertificate "github.com/submariner-io/admiral/pkg/certificate/fake"
	"github.com/submariner-io/admiral/pkg/fake"
	"github.com/submariner-io/admiral/pkg/federate"
	. "github.com/submariner-io/admiral/pkg/gomega"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	testutil "github.com/submariner-io/admiral/pkg/test"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	fakecable "github.com/submariner-io/submariner/pkg/cable/fake"
	"github.com/submariner-io/submariner/pkg/cableengine"
	enginefake "github.com/submariner-io/submariner/pkg/cableengine/fake"
	submfake "github.com/submariner-io/submariner/pkg/client/clientset/versioned/fake"
	submendpoint "github.com/submariner-io/submariner/pkg/endpoint"
	"github.com/submariner-io/submariner/pkg/gateway"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	"github.com/submariner-io/submariner/pkg/types"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	k8snet "k8s.io/utils/net"
)

const publicIP = "1.2.3.4"

var _ = Describe("Run", func() {
	t := newTestDriver()

	It("should start the controllers", func(ctx context.Context) {
		t.leaderElection.AwaitLeaseAcquired()
		t.awaitLocalEndpoint(ctx)
		t.awaitHAStatus(ctx, submarinerv1.HAStatusActive)

		t.cableEngine.Lock()
		t.cableEngine.HAStatus = submarinerv1.HAStatusActive
		t.cableEngine.Unlock()

		t.awaitGateway(ctx, func(gw *submarinerv1.Gateway) bool {
			return gw.Status.HAStatus == submarinerv1.HAStatusActive
		})

		endpoint := t.awaitRemoteEndpointSyncedLocal(ctx, t.createRemoteEndpointOnBroker(ctx))
		t.cableEngine.VerifyInstallCable(&endpoint.Spec)
	})

	When("starting the Cable Engine fails", func() {
		BeforeEach(func() {
			t.expectedRunErr = errors.New("mock Cable Engine Start error")
			t.cableEngine.ErrOnStart = t.expectedRunErr
		})

		It("should return an error", func() {
		})
	})

	When("renewal of the leader lease fails", func() {
		BeforeEach(func() {
			fakeDriver = fakecable.New()
			fakeDriver.Connections = []submarinerv1.Connection{
				{
					Status: submarinerv1.Connected,
					Endpoint: submarinerv1.EndpointSpec{
						CableName: "submariner-cable-north-5-5-5-5",
					},
					UsingIP:  "5.6.7.8",
					UsingNAT: true,
				},
			}

			t.config.NewCableEngine = cableengine.NewEngine
			t.config.RenewDeadline = time.Millisecond * 200
			t.config.RetryPeriod = time.Millisecond * 20
		})

		JustBeforeEach(func(ctx context.Context) {
			t.leaderElection.AwaitLeaseAcquired()
			t.awaitHAStatus(ctx, submarinerv1.HAStatusActive)

			t.awaitGateway(ctx, func(gw *submarinerv1.Gateway) bool {
				return gw.Status.HAStatus == submarinerv1.HAStatusActive && reflect.DeepEqual(gw.Status.Connections, fakeDriver.Connections)
			})
		})

		It("should re-acquire the leader lease after the failure is cleared", func(ctx context.Context) {
			endpoint := t.awaitRemoteEndpointSyncedLocal(ctx, t.createRemoteEndpointOnBroker(ctx))
			fakeDriver.AwaitConnectToEndpoint(&natdiscovery.NATEndpointInfo{
				Endpoint:  *endpoint,
				UseFamily: k8snet.IPv4,
			})

			By("Setting leases resource updates to fail")

			fake.FailOnAction(&t.kubeClient.Fake, "pods", "patch", apierrors.NewTooManyRequests("too many requests", 1), true)

			t.leaderElection.FailLease(t.config.RenewDeadline)

			By("Ensuring controllers are stopped")

			t.awaitHAStatus(ctx, submarinerv1.HAStatusPassive)

			// The Gateway status should reflect that the CableEngine is stopped.
			t.awaitGateway(ctx, func(gw *submarinerv1.Gateway) bool {
				return gw.Status.HAStatus == submarinerv1.HAStatusPassive && len(gw.Status.Connections) == 0
			})

			// Ensure the datastore syncer is stopped.
			brokerEndpoint := t.createRemoteEndpointOnBroker(ctx)
			t.ensureNoRemoteEndpointSyncedLocal(ctx, brokerEndpoint)

			// Ensure the tunnel controller is stopped.
			Expect(t.endpoints.Namespace(t.config.Spec.Namespace).Delete(ctx, endpoint.Name, metav1.DeleteOptions{})).
				To(Succeed())
			fakeDriver.AwaitNoDisconnectFromEndpoint()

			// Delete the endpoint from the broker and recreate locally to simulate a stale remote endpoint.
			Expect(t.endpoints.Namespace(t.config.SyncerConfig.BrokerNamespace).Delete(ctx, endpoint.Name,
				metav1.DeleteOptions{})).To(Succeed())
			t.createEndpoint(ctx, t.config.Spec.Namespace, endpoint)

			By("Setting leases resource updates to succeed")

			t.leaderElection.SucceedLease()

			By("Ensuring lease was renewed")

			t.leaderElection.AwaitLeaseRenewed()

			By("Ensuring controllers are restarted")

			t.awaitHAStatus(ctx, submarinerv1.HAStatusActive)

			// The Gateway status should reflect that the CableEngine is re-started.
			t.awaitGateway(ctx, func(gw *submarinerv1.Gateway) bool {
				return gw.Status.HAStatus == submarinerv1.HAStatusActive && reflect.DeepEqual(gw.Status.Connections, fakeDriver.Connections)
			})

			endpoint2 := t.awaitRemoteEndpointSyncedLocal(ctx, brokerEndpoint)
			fakeDriver.AwaitConnectToEndpoint(&natdiscovery.NATEndpointInfo{
				Endpoint:  *endpoint2,
				UseFamily: k8snet.IPv4,
			})

			fakeDriver.AwaitDisconnectFromEndpoint(&endpoint.Spec, k8snet.IPv4)
		})
	})

	Context("on uninstall", func() {
		BeforeEach(func(ctx context.Context) {
			t.config.Spec.Uninstall = true

			hostName, err := os.Hostname()
			Expect(err).To(Succeed())

			_, err = t.config.SubmarinerClient.SubmarinerV1().Gateways(t.config.Spec.Namespace).Create(ctx,
				&submarinerv1.Gateway{
					ObjectMeta: metav1.ObjectMeta{
						Name: hostName,
					},
				}, metav1.CreateOptions{})
			Expect(err).To(Succeed())

			test.CreateResource(ctx, t.endpoints.Namespace(t.config.Spec.Namespace), &submarinerv1.Endpoint{
				ObjectMeta: metav1.ObjectMeta{
					Name: "some-endpoint",
				},
			})
		})

		It("should perform cleanup", func(ctx context.Context) {
			t.awaitNoEndpoints(ctx)
			t.cableEngine.AwaitCleanup()
			t.signingRequestor.AwaitUninstall()
		})

		When("an error occurs", func() {
			BeforeEach(func() {
				t.expectedRunErr = errors.New("mock datastore syncer error")
				fake.FailOnAction(&t.dynClient.Fake, "endpoints", "delete", t.expectedRunErr, false)

				t.cableEngine.ErrOnCleanup = errors.New("mock Cable Engine Cleanup error")
			})

			It("should return the error", func() {
			})
		})
	})
})

type testDriver struct {
	config           gateway.Config
	localPodName     string
	nodeName         string
	endpoints        dynamic.NamespaceableResourceInterface
	expectedRunErr   error
	cableEngine      *enginefake.Engine
	signingRequestor *fakecertificate.SigningRequestor
	kubeClient       *k8sfake.Clientset
	dynClient        *dynamicfake.FakeDynamicClient
	leaderElection   *testutil.LeaderElectionSupport
	remoteIPCounter  int
}

func newTestDriver() *testDriver {
	t := &testDriver{}

	BeforeEach(func(ctx context.Context) {
		t.expectedRunErr = nil
		t.remoteIPCounter = 1

		restMapper := test.GetRESTMapperFor(&submarinerv1.Endpoint{}, &submarinerv1.Cluster{},
			&submarinerv1.Gateway{}, &corev1.Node{}, &corev1.Secret{})

		t.dynClient = dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
		t.kubeClient = k8sfake.NewClientset()

		t.cableEngine = enginefake.New()

		t.signingRequestor = fakecertificate.NewSigningRequestor()

		t.config = gateway.Config{
			Spec: types.SubmarinerSpecification{
				ClusterCidr:        []string{"169.254.1.0/24"},
				GlobalCidr:         []string{"224.0.0.0/16"},
				ServiceCidr:        []string{"169.254.2.0/24"},
				ClusterID:          "east",
				Namespace:          "submariner",
				PublicIP:           "ipv4:" + publicIP,
				HealthCheckEnabled: true,
				CableDriver:        fakecable.DriverName,
			},
			SyncerConfig: broker.SyncerConfig{
				LocalRestConfig: &rest.Config{
					Host: "https://localhost:6443",
				},
				BrokerRestConfig: &rest.Config{
					Host: "https://localhost:6443",
				},
				LocalClient:     t.dynClient,
				BrokerClient:    t.dynClient,
				BrokerNamespace: "broker-ns",
				RestMapper:      restMapper,
				Scheme:          scheme.Scheme,
			},
			WatcherConfig: watcher.Config{
				RestMapper: restMapper,
				Client:     t.dynClient,
			},
			SubmarinerClient:     submfake.NewSimpleClientset(),
			KubeClient:           t.kubeClient,
			LeaderElectionClient: t.kubeClient,
			NewCableEngine: func(_ *types.SubmarinerCluster, lep *submendpoint.Local) cableengine.Engine {
				t.cableEngine.LocalEndPoint = &types.SubmarinerEndpoint{Spec: *lep.Spec()}
				return t.cableEngine
			},
			NewNATDiscovery: func(_ *submendpoint.Local) (natdiscovery.Interface, error) {
				return &fakeNATDiscovery{}, nil
			},
			StartSigningRequestor: func(_ context.Context, _ broker.SyncerConfig, _ <-chan struct{}) (certificate.SigningRequestor, error) {
				return t.signingRequestor, nil
			},
		}

		t.leaderElection = testutil.NewLeaderElectionSupport(t.kubeClient, t.config.Spec.Namespace, gateway.LeaderElectionLockName)

		t.endpoints = t.config.SyncerConfig.LocalClient.Resource(*test.GetGroupVersionResourceFor(restMapper, &submarinerv1.Endpoint{}))

		t.localPodName = "local-pod"
		t.nodeName = "raiders"

		os.Setenv("SUBMARINER_NAMESPACE", t.config.Spec.Namespace)
		os.Setenv("NODE_NAME", t.nodeName)
		os.Setenv("POD_NAME", t.localPodName)

		_, err := t.config.KubeClient.CoreV1().Nodes().Create(ctx, &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: t.nodeName,
			},
		}, metav1.CreateOptions{})
		Expect(err).To(Succeed())

		_, err = t.config.KubeClient.CoreV1().Pods(t.config.Spec.Namespace).Create(ctx, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: t.localPodName,
			},
		}, metav1.CreateOptions{})
		Expect(err).To(Succeed())
	})

	JustBeforeEach(func(ctx context.Context) {
		gw, err := gateway.New(ctx, &t.config)
		Expect(err).To(Succeed())

		ctx, stop := context.WithCancel(context.Background()) //nolint:contextcheck // Need a long-lived parent context
		runCompleted := make(chan error, 1)

		DeferCleanup(func(ctx context.Context) {
			if t.expectedRunErr == nil {
				stop()
			}

			err := func() error {
				timeout := 5 * time.Second
				select {
				case err := <-runCompleted:
					return errors.WithMessage(err, "Run returned an error")
				case <-time.After(timeout):
					return fmt.Errorf("Run did not complete after %v", timeout)
				}
			}()

			if t.expectedRunErr == nil {
				Expect(err).To(Succeed())
				t.awaitNoGateway(ctx)
			} else {
				Expect(err).To(ContainErrorSubstring(t.expectedRunErr))

				if !t.config.Spec.Uninstall {
					t.awaitGatewayStatusError(ctx, t.expectedRunErr.Error())
				}
			}

			if !t.config.Spec.Uninstall {
				t.awaitHAStatus(ctx, submarinerv1.HAStatusPassive)
			}
		})

		go func() {
			runCompleted <- gw.Run(ctx)
		}()
	})

	return t
}

func toEndpoint(from *unstructured.Unstructured) *submarinerv1.Endpoint {
	endpoint := &submarinerv1.Endpoint{}
	Expect(scheme.Scheme.Convert(from, endpoint, nil)).To(Succeed())

	return endpoint
}

func (t *testDriver) awaitLocalEndpoint(ctx context.Context) {
	Eventually(func(ctx context.Context) bool {
		l, err := t.endpoints.Namespace(t.config.Spec.Namespace).List(ctx, metav1.ListOptions{})
		Expect(err).To(Succeed())

		for i := range l.Items {
			endpoint := toEndpoint(&l.Items[i])

			if endpoint.Spec.ClusterID == t.config.Spec.ClusterID {
				Expect(endpoint.Spec.PublicIPs).To(Equal([]string{publicIP}))
				Expect(endpoint.Spec.Backend).To(Equal(fakecable.DriverName))
				Expect(endpoint.Spec.Subnets).To(Equal(t.config.Spec.GlobalCidr))

				return true
			}
		}

		return false
	}).Within(3 * time.Second).WithContext(ctx).Should(BeTrue())
}

func (t *testDriver) awaitNoEndpoints(ctx context.Context) {
	Eventually(func() int {
		l, err := t.endpoints.Namespace(t.config.Spec.Namespace).List(ctx, metav1.ListOptions{})
		Expect(err).To(Succeed())

		return len(l.Items)
	}, 3).Should(BeZero())
}

func (t *testDriver) newRemoteEndpoint() *submarinerv1.Endpoint {
	ep := &submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name:   string(uuid.NewUUID()),
			Labels: map[string]string{federate.ClusterIDLabelKey: "west"},
		},
		Spec: submarinerv1.EndpointSpec{
			ClusterID:  "west",
			CableName:  fmt.Sprintf("submariner-cable-west-192-168-40-%d", t.remoteIPCounter),
			Hostname:   "redsox",
			Subnets:    []string{"169.254.3.0/24"},
			PrivateIPs: []string{"11.1.2.3"},
			PublicIPs:  []string{"ipv4:12.1.2.3"},
			Backend:    "libreswan",
		},
	}

	t.remoteIPCounter++

	return ep
}

func (t *testDriver) createEndpoint(ctx context.Context, ns string, endpoint *submarinerv1.Endpoint) *submarinerv1.Endpoint {
	return test.CreateResource(ctx, t.endpoints.Namespace(ns), endpoint)
}

func (t *testDriver) createRemoteEndpointOnBroker(ctx context.Context) *submarinerv1.Endpoint {
	return t.createEndpoint(ctx, t.config.SyncerConfig.BrokerNamespace, t.newRemoteEndpoint())
}

func (t *testDriver) awaitRemoteEndpointSyncedLocal(ctx context.Context, endpoint *submarinerv1.Endpoint) *submarinerv1.Endpoint {
	return toEndpoint(test.AwaitResource(ctx, t.endpoints.Namespace(t.config.Spec.Namespace), endpoint.Name))
}

func (t *testDriver) ensureNoRemoteEndpointSyncedLocal(ctx context.Context, endpoint *submarinerv1.Endpoint) {
	testutil.EnsureNoResource(ctx, resource.ForDynamic(t.endpoints.Namespace(t.config.Spec.Namespace)), endpoint.Name)
}

func (t *testDriver) awaitHAStatus(ctx context.Context, status submarinerv1.HAStatus) {
	Eventually(func(g Gomega, ctx context.Context) {
		pod, err := t.config.KubeClient.CoreV1().Pods(t.config.Spec.Namespace).Get(ctx, t.localPodName, metav1.GetOptions{})
		g.Expect(err).To(Succeed())
		g.Expect(pod.Labels["gateway.submariner.io/status"]).To(Equal(string(status)))
	}).Within(3 * time.Second).WithContext(ctx).Should(Succeed())
}

func (t *testDriver) awaitGateway(ctx context.Context, verify func(*submarinerv1.Gateway) bool) {
	Eventually(func(ctx context.Context) []submarinerv1.Gateway {
		l, err := t.config.SubmarinerClient.SubmarinerV1().Gateways(t.config.Spec.Namespace).List(ctx, metav1.ListOptions{})
		Expect(err).To(Succeed())

		if len(l.Items) == 1 && (verify == nil || verify(&l.Items[0])) {
			return nil
		}

		return l.Items
	}).Within(3 * time.Second).WithContext(ctx).Should(BeEmpty())
}

func (t *testDriver) awaitNoGateway(ctx context.Context) {
	Eventually(func(g Gomega, ctx context.Context) {
		l, err := t.config.SubmarinerClient.SubmarinerV1().Gateways(t.config.Spec.Namespace).List(ctx, metav1.ListOptions{})
		g.Expect(err).To(Succeed())
		g.Expect(l.Items).Should(BeEmpty())
	}).Within(3 * time.Second).WithContext(ctx).Should(Succeed())
}

func (t *testDriver) awaitGatewayStatusError(ctx context.Context, s string) {
	Eventually(func(g Gomega, ctx context.Context) {
		l, err := t.config.SubmarinerClient.SubmarinerV1().Gateways(t.config.Spec.Namespace).List(ctx, metav1.ListOptions{})
		g.Expect(err).To(Succeed())
		g.Expect(l.Items).To(HaveLen(1))
		g.Expect(l.Items[0].Status.StatusFailure).To(ContainSubstring(s))
	}).Within(3 * time.Second).WithContext(ctx).Should(Succeed())
}

type fakeNATDiscovery struct {
	readyChannel chan *natdiscovery.NATEndpointInfo
}

func (n *fakeNATDiscovery) Run(_ <-chan struct{}) error {
	return nil
}

func (n *fakeNATDiscovery) AddEndpoint(ep *submarinerv1.Endpoint, family k8snet.IPFamily) {
	n.readyChannel <- &natdiscovery.NATEndpointInfo{
		Endpoint:  *ep,
		UseFamily: family,
	}
}

func (n *fakeNATDiscovery) RemoveEndpoint(_ string) {
}

func (n *fakeNATDiscovery) GetReadyChannel() chan *natdiscovery.NATEndpointInfo {
	n.readyChannel = make(chan *natdiscovery.NATEndpointInfo, 100)
	return n.readyChannel
}
