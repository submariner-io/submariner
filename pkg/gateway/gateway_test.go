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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/fake"
	. "github.com/submariner-io/admiral/pkg/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cableengine"
	enginefake "github.com/submariner-io/submariner/pkg/cableengine/fake"
	submfake "github.com/submariner-io/submariner/pkg/client/clientset/versioned/fake"
	"github.com/submariner-io/submariner/pkg/gateway"
	"github.com/submariner-io/submariner/pkg/natdiscovery"
	"github.com/submariner-io/submariner/pkg/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
)

var _ = Describe("Run", func() {
	t := newTestDriver()

	It("should start the controllers", func() {
		t.awaitEndpoint()
		t.awaitHAStatus(submarinerv1.HAStatusActive)
		t.awaitGateway()
	})

	When("starting the Cable Engine fails", func() {
		BeforeEach(func() {
			t.expectedRunErr = errors.New("mock Cable Engine Start error")
			t.cableEngine.StartErr = t.expectedRunErr
		})

		It("should return an error", func() {
		})
	})

	When("renewal of the leader lock fails", func() {
		var leasesReactor *fake.FailOnActionReactor

		BeforeEach(func() {
			t.config.RenewDeadline = time.Millisecond * 200
			t.config.RetryPeriod = time.Millisecond * 20

			t.expectedRunErr = errors.New("leader election lost")
			leasesReactor = fake.FailOnAction(&t.kubeClient.Fake, "leases", "update", nil, false)
			leasesReactor.Fail(false)
		})

		It("should return an error", func() {
			t.awaitHAStatus(submarinerv1.HAStatusActive)

			By("Setting leases resource updates to fail")

			leasesReactor.Fail(true)
		})
	})
})

type testDriver struct {
	config         gateway.Config
	localPodName   string
	nodeName       string
	endpoints      dynamic.NamespaceableResourceInterface
	expectedRunErr error
	cableEngine    *enginefake.Engine
	kubeClient     *k8sfake.Clientset
}

func newTestDriver() *testDriver {
	t := &testDriver{}

	BeforeEach(func() {
		t.expectedRunErr = nil

		restMapper := test.GetRESTMapperFor(&submarinerv1.Endpoint{}, &submarinerv1.Cluster{}, &submarinerv1.Gateway{}, &corev1.Node{})

		dynClient := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
		t.kubeClient = k8sfake.NewSimpleClientset()

		t.cableEngine = enginefake.New()

		t.config = gateway.Config{
			Spec: types.SubmarinerSpecification{
				ClusterCidr:        []string{"169.254.1.0/24"},
				GlobalCidr:         []string{"224.0.0.0/16"},
				ServiceCidr:        []string{"169.254.2.0/24"},
				ClusterID:          "east",
				Namespace:          "submariner",
				PublicIP:           "ipv4:1.2.3.4",
				HealthCheckEnabled: true,
			},
			SyncerConfig: broker.SyncerConfig{
				LocalClient:     dynClient,
				BrokerClient:    dynClient,
				BrokerNamespace: "broker-ns",
				RestMapper:      restMapper,
			},
			WatcherConfig: watcher.Config{
				RestMapper: restMapper,
				Client:     dynClient,
			},
			SubmarinerClient:     submfake.NewSimpleClientset(),
			KubeClient:           t.kubeClient,
			LeaderElectionClient: t.kubeClient,
			NewCableEngine: func(_ *types.SubmarinerCluster, ep *types.SubmarinerEndpoint) cableengine.Engine {
				t.cableEngine.LocalEndPoint = ep
				return t.cableEngine
			},
			NewNATDiscovery: func(_ *types.SubmarinerEndpoint) (natdiscovery.Interface, error) {
				return &fakeNATDiscovery{}, nil
			},
		}

		t.endpoints = t.config.SyncerConfig.LocalClient.Resource(*test.GetGroupVersionResourceFor(restMapper, &submarinerv1.Endpoint{}))

		t.localPodName = "local-pod"
		t.nodeName = "raiders"

		os.Setenv("SUBMARINER_NAMESPACE", t.config.Spec.Namespace)
		os.Setenv("NODE_NAME", t.nodeName)
		os.Setenv("POD_NAME", t.localPodName)

		_, err := t.config.KubeClient.CoreV1().Nodes().Create(context.Background(), &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: t.nodeName,
			},
		}, metav1.CreateOptions{})
		Expect(err).To(Succeed())

		_, err = t.config.KubeClient.CoreV1().Pods(t.config.Spec.Namespace).Create(context.Background(), &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: t.localPodName,
			},
		}, metav1.CreateOptions{})
		Expect(err).To(Succeed())
	})

	JustBeforeEach(func() {
		gw, err := gateway.New(&t.config)
		Expect(err).To(Succeed())

		stopCh := make(chan struct{})
		runCompleted := make(chan error, 1)

		DeferCleanup(func() {
			if t.expectedRunErr == nil {
				close(stopCh)
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
				t.awaitNoGateway()
			} else {
				Expect(err).To(ContainErrorSubstring(t.expectedRunErr))
				t.awaitHAStatus(submarinerv1.HAStatusPassive)
				t.awaitGatewayStatusError(t.expectedRunErr.Error())
			}
		})

		go func() {
			runCompleted <- gw.Run(stopCh)
		}()
	})

	return t
}

func (t *testDriver) awaitEndpoint() {
	Eventually(func() int {
		l, err := t.endpoints.Namespace(t.config.Spec.Namespace).List(context.Background(), metav1.ListOptions{})
		Expect(err).To(Succeed())

		return len(l.Items)
	}, 3).Should(Equal(1))
}

func (t *testDriver) awaitHAStatus(status submarinerv1.HAStatus) {
	Eventually(func() string {
		pod, err := t.config.KubeClient.CoreV1().Pods(t.config.Spec.Namespace).Get(context.Background(), t.localPodName, metav1.GetOptions{})
		Expect(err).To(Succeed())

		return pod.Labels["gateway.submariner.io/status"]
	}, 3).Should(Equal(string(status)))
}

func (t *testDriver) awaitGateway() {
	Eventually(func() int {
		l, err := t.config.SubmarinerClient.SubmarinerV1().Gateways(t.config.Spec.Namespace).List(context.Background(), metav1.ListOptions{})
		Expect(err).To(Succeed())

		return len(l.Items)
	}, 3).Should(Equal(1))
}

func (t *testDriver) awaitNoGateway() {
	Eventually(func() int {
		l, err := t.config.SubmarinerClient.SubmarinerV1().Gateways(t.config.Spec.Namespace).List(context.Background(), metav1.ListOptions{})
		Expect(err).To(Succeed())

		return len(l.Items)
	}, 3).Should(BeZero())
}

func (t *testDriver) awaitGatewayStatusError(s string) {
	Eventually(func() string {
		l, err := t.config.SubmarinerClient.SubmarinerV1().Gateways(t.config.Spec.Namespace).List(context.Background(), metav1.ListOptions{})
		Expect(err).To(Succeed())
		Expect(l.Items).To(HaveLen(1))

		return l.Items[0].Status.StatusFailure
	}, 3).Should(ContainSubstring(s))
}

type fakeNATDiscovery struct{}

func (n *fakeNATDiscovery) Run(_ <-chan struct{}) error {
	return nil
}

func (n *fakeNATDiscovery) AddEndpoint(_ *submarinerv1.Endpoint) {
}

func (n *fakeNATDiscovery) RemoveEndpoint(_ string) {
}

func (n *fakeNATDiscovery) GetReadyChannel() chan *natdiscovery.NATEndpointInfo {
	return make(chan *natdiscovery.NATEndpointInfo, 100)
}
