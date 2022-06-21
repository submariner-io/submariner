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

package syncer_test

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	gomegaTypes "github.com/onsi/gomega/types"
	"github.com/pkg/errors"
	fakeReactor "github.com/submariner-io/admiral/pkg/fake"
	. "github.com/submariner-io/admiral/pkg/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	fakeEngine "github.com/submariner-io/submariner/pkg/cableengine/fake"
	"github.com/submariner-io/submariner/pkg/cableengine/healthchecker"
	"github.com/submariner-io/submariner/pkg/cableengine/healthchecker/fake"
	"github.com/submariner-io/submariner/pkg/cableengine/syncer"
	fakeClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned/fake"
	submarinerClientsetv1 "github.com/submariner-io/submariner/pkg/client/clientset/versioned/typed/submariner.io/v1"
	submarinerInformers "github.com/submariner-io/submariner/pkg/client/informers/externalversions"
	"github.com/submariner-io/submariner/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	fakeClient "k8s.io/client-go/dynamic/fake"
	kubeScheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

const (
	namespace = "submariner"
)

func init() {
	klog.InitFlags(nil)
}

var _ = BeforeSuite(func() {
	syncer.GatewayUpdateInterval = 200 * time.Millisecond
	syncer.GatewayStaleTimeout = 1 * time.Second
})

var _ = Describe("", func() {
	Context("Gateway syncing", testGatewaySyncing)
	Context("Stale Gateway cleanup", testStaleGatewayCleanup)
	Context("Gateway sync errors", testGatewaySyncErrors)
	Context("Gateway latency info", testGatewayLatencyInfo)
})

func testGatewaySyncing() {
	var t *testDriver

	BeforeEach(func() {
		t = newTestDriver()
	})

	JustBeforeEach(func() {
		t.run()
	})

	AfterEach(func() {
		t.stop()
	})

	When("the syncer is started", func() {
		It("should create the Gateway resource with the correct information", func() {
			t.awaitGatewayUpdated(t.expectedGateway)
		})
	})

	When("the cable engine info changes", func() {
		BeforeEach(func() {
			t.engine.Connections = nil
		})

		It("should update the Gateway Status with the correct information", func() {
			t.awaitGatewayUpdated(t.expectedGateway)

			t.engine.Lock()

			t.expectedGateway.Status.HAStatus = submarinerv1.HAStatusActive
			t.engine.HAStatus = t.expectedGateway.Status.HAStatus

			t.expectedGateway.Status.Connections = []submarinerv1.Connection{
				{
					Status:        submarinerv1.Connecting,
					StatusMessage: "Connecting to 1.2.3.4:400",
					Endpoint: submarinerv1.EndpointSpec{
						ClusterID: "west",
						CableName: "submariner-cable-west-192-68-1-10",
						PrivateIP: "192.6.1.11",
						Backend:   "libreswan",
					},
				},
				{
					Status:        submarinerv1.Connected,
					StatusMessage: "Connected to 1.2.3.5:500",
					Endpoint: submarinerv1.EndpointSpec{
						ClusterID: "north",
						CableName: "submariner-cable-north-192-68-1-20",
						PrivateIP: "192.6.1.21",
						Backend:   "wireguard",
					},
				},
			}
			t.engine.Connections = t.expectedGateway.Status.Connections

			t.engine.Unlock()

			t.awaitGatewayUpdated(t.expectedGateway)
			t.awaitNoGatewayUpdated()
		})
	})

	When("a specific status error is set", func() {
		It("should create the Gateway resource with the correct StatusFailure", func() {
			t.awaitGatewayUpdated(t.expectedGateway)

			statusErr := errors.New("fake error")
			t.expectedGateway.Status.StatusFailure = statusErr.Error()

			t.syncer.SetGatewayStatusError(statusErr)
			t.awaitGatewayUpdated(t.expectedGateway)
		})
	})
}

func testStaleGatewayCleanup() {
	var t *testDriver
	var staleGateway *submarinerv1.Gateway

	expectedErr := errors.New("fake error")

	BeforeEach(func() {
		t = newTestDriver()
		staleGateway = &submarinerv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{
				Name: "raiders",
			},
			Status: submarinerv1.GatewayStatus{
				HAStatus: submarinerv1.HAStatusPassive,
			},
		}

		t.expectedGateway.Status.HAStatus = submarinerv1.HAStatusActive
		t.engine.HAStatus = t.expectedGateway.Status.HAStatus
	})

	JustBeforeEach(func() {
		t.run()

		t.awaitGatewayUpdated(t.expectedGateway)

		_, err := t.gateways.Create(context.TODO(), staleGateway, metav1.CreateOptions{})
		Expect(err).To(Succeed())

		t.awaitGatewayUpdated(staleGateway)
	})

	AfterEach(func() {
		t.stop()
	})

	When("the Gateway's update timestamp expires", func() {
		BeforeEach(func() {
			staleGateway.Annotations = map[string]string{"update-timestamp": strconv.FormatInt(time.Now().UTC().Unix(), 10)}
		})

		It("should delete the Gateway", func() {
			t.awaitGatewayDeleted(staleGateway)
			t.awaitNoGatewayDeleted()
		})
	})

	When("the Gateway's update-timestamp annotations is missing", func() {
		BeforeEach(func() {
			staleGateway.Annotations = map[string]string{}
		})

		It("should delete the Gateway", func() {
			t.awaitGatewayDeleted(staleGateway)
		})
	})

	When("the Gateway's annotations are missing", func() {
		It("should delete the Gateway", func() {
			t.awaitGatewayDeleted(staleGateway)
		})
	})

	When("the Gateway's update-timestamp annotation is invalid", func() {
		BeforeEach(func() {
			staleGateway.Annotations = map[string]string{"update-timestamp": "invalid"}
		})

		It("should delete the Gateway", func() {
			t.awaitGatewayDeleted(staleGateway)
		})
	})

	When("listing of Gateways fails", func() {
		JustBeforeEach(func() {
			t.gatewayReactor.SetFailOnList(expectedErr)
		})

		It("should log the error", func() {
			Eventually(t.handledError, 5).Should(Receive(ContainErrorSubstring(expectedErr)))
		})
	})

	When("Gateway delete fails", func() {
		BeforeEach(func() {
			t.gatewayReactor.SetFailOnDelete(expectedErr)
			t.expectedDeletedAfter = nil
		})

		It("should log the error", func() {
			Eventually(t.handledError, 5).Should(Receive(ContainErrorSubstring(expectedErr)))
		})
	})
}

func testGatewaySyncErrors() {
	var t *testDriver
	var expectedErr error

	BeforeEach(func() {
		t = newTestDriver()
		expectedErr = errors.New("fake error")
		t.expectedDeletedAfter = nil
	})

	JustBeforeEach(func() {
		t.run()
	})

	AfterEach(func() {
		t.stop()
	})

	When("Gateway create fails", func() {
		BeforeEach(func() {
			t.gatewayReactor.SetFailOnCreate(expectedErr)
		})

		It("should log the error", func() {
			Eventually(t.handledError, 5).Should(Receive(ContainErrorSubstring(expectedErr)))
		})
	})

	When("Gateway update fails", func() {
		BeforeEach(func() {
			t.gatewayReactor.SetFailOnUpdate(expectedErr)
		})

		It("should log the error", func() {
			t.awaitGatewayUpdated(t.expectedGateway)

			t.engine.Lock()
			t.engine.HAStatus = submarinerv1.HAStatusActive
			t.engine.Unlock()

			Eventually(t.handledError, 5).Should(Receive(ContainErrorSubstring(expectedErr)))
		})
	})

	When("existing Gateway retrieval fails", func() {
		BeforeEach(func() {
			t.gatewayReactor.SetFailOnGet(expectedErr)
		})

		It("should log the error", func() {
			Eventually(t.handledError, 5).Should(Receive(ContainErrorSubstring(expectedErr)))
		})
	})

	When("listing of cable engine connections fails", func() {
		BeforeEach(func() {
			t.engine.ListCableConnectionsError = expectedErr
			t.expectedGateway.Status.StatusFailure = expectedErr.Error()
		})

		It("update the Gateway Status failure", func() {
			t.awaitGatewayUpdated(t.expectedGateway)
		})
	})
}

func testGatewayLatencyInfo() {
	var t *testDriver

	BeforeEach(func() {
		t = newTestDriver()
	})

	JustBeforeEach(func() {
		t.run()
	})

	AfterEach(func() {
		t.stop()
	})

	When("the health checker provides latency info", func() {
		It("should correctly update the Gateway Status information", func() {
			t.awaitGatewayUpdated(t.expectedGateway)

			endpointSpec := &submarinerv1.EndpointSpec{
				ClusterID:     "north",
				CableName:     "submariner-cable-north-192-68-1-20",
				PrivateIP:     "192-68-1-20",
				HealthCheckIP: t.pinger.GetIP(),
			}

			endpointName, err := endpointSpec.GenerateName()
			Expect(err).To(Succeed())

			test.CreateResource(t.endpoints, &submarinerv1.Endpoint{
				ObjectMeta: metav1.ObjectMeta{
					Name: endpointName,
				},
				Spec: *endpointSpec,
			})

			t.engine.Lock()

			t.expectedGateway.Status.HAStatus = submarinerv1.HAStatusActive
			t.engine.HAStatus = t.expectedGateway.Status.HAStatus

			t.expectedGateway.Status.Connections = []submarinerv1.Connection{
				{
					Status:   submarinerv1.Connected,
					Endpoint: *endpointSpec,
				},
			}

			t.engine.Connections = []submarinerv1.Connection{t.expectedGateway.Status.Connections[0]}

			t.expectedGateway.Status.Connections[0].LatencyRTT = &submarinerv1.LatencyRTTSpec{
				Last:    "93ms",
				Min:     "90ms",
				Average: "95ms",
				Max:     "100ms",
				StdDev:  "94ms",
			}

			t.pinger.SetLatencyInfo(&healthchecker.LatencyInfo{
				ConnectionStatus: healthchecker.Connected,
				Spec:             t.expectedGateway.Status.Connections[0].LatencyRTT,
			})

			t.engine.Unlock()

			t.awaitGatewayUpdated(t.expectedGateway)

			t.expectedGateway.Status.Connections[0].Status = submarinerv1.ConnectionError
			t.expectedGateway.Status.Connections[0].StatusMessage = "Ping failed"

			t.pinger.SetLatencyInfo(&healthchecker.LatencyInfo{
				ConnectionStatus: healthchecker.ConnectionError,
				ConnectionError:  t.expectedGateway.Status.Connections[0].StatusMessage,
				Spec:             t.expectedGateway.Status.Connections[0].LatencyRTT,
			})

			t.awaitGatewayUpdated(t.expectedGateway)

			t.expectedGateway.Status.Connections[0].Status = submarinerv1.Connected
			t.expectedGateway.Status.Connections[0].StatusMessage = ""

			t.pinger.SetLatencyInfo(&healthchecker.LatencyInfo{
				ConnectionStatus: healthchecker.Connected,
				Spec:             t.expectedGateway.Status.Connections[0].LatencyRTT,
			})

			t.awaitGatewayUpdated(t.expectedGateway)
		})
	})
}

type testDriver struct {
	engine               *fakeEngine.Engine
	client               *fakeClientset.Clientset
	gateways             submarinerClientsetv1.GatewayInterface
	gatewayReactor       *fakeReactor.FailingReactor
	syncer               *syncer.GatewaySyncer
	healthChecker        healthchecker.Interface
	pinger               *fake.Pinger
	endpoints            dynamic.ResourceInterface
	expectedGateway      *submarinerv1.Gateway
	expectedDeletedAfter *submarinerv1.Gateway
	gatewayUpdated       chan *submarinerv1.Gateway
	gatewayDeleted       chan *submarinerv1.Gateway
	stopSyncer           chan struct{}
	stopInformer         chan struct{}
	savedErrorHandlers   []func(error)
	handledError         chan error
}

func newTestDriver() *testDriver {
	client := fakeClientset.NewSimpleClientset()

	t := &testDriver{
		engine:             fakeEngine.New(),
		client:             client,
		gateways:           client.SubmarinerV1().Gateways(namespace),
		gatewayReactor:     fakeReactor.NewFailingReactorForResource(&client.Fake, "gateways"),
		gatewayUpdated:     make(chan *submarinerv1.Gateway, 10),
		gatewayDeleted:     make(chan *submarinerv1.Gateway, 10),
		stopSyncer:         make(chan struct{}),
		stopInformer:       make(chan struct{}),
		savedErrorHandlers: utilruntime.ErrorHandlers,
		handledError:       make(chan error, 10),
	}

	t.engine.LocalEndPoint = &types.SubmarinerEndpoint{Spec: submarinerv1.EndpointSpec{
		ClusterID: "east",
		CableName: "submariner-cable-east-192-68-1-2",
		Hostname:  "redsox",
		PrivateIP: "192.6.1.3",
		Backend:   "libreswan",
	}}

	t.expectedGateway = &submarinerv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name: t.engine.LocalEndPoint.Spec.Hostname,
		},
		Status: submarinerv1.GatewayStatus{
			Version:       "1",
			HAStatus:      t.engine.GetHAStatus(),
			LocalEndpoint: t.engine.LocalEndPoint.Spec,
			Connections:   t.engine.Connections,
		},
	}

	t.expectedDeletedAfter = t.expectedGateway

	return t
}

func (t *testDriver) run() {
	utilruntime.ErrorHandlers = append(utilruntime.ErrorHandlers, func(err error) {
		t.handledError <- err
	})

	Expect(submarinerv1.AddToScheme(kubeScheme.Scheme)).To(Succeed())

	scheme := runtime.NewScheme()
	Expect(submarinerv1.AddToScheme(scheme)).To(Succeed())

	dynamicClient := fakeClient.NewSimpleDynamicClient(scheme)
	restMapper := test.GetRESTMapperFor(&submarinerv1.Endpoint{})

	t.pinger = fake.NewPinger("10.130.2.2")

	t.healthChecker, _ = healthchecker.New(&healthchecker.Config{
		WatcherConfig: &watcher.Config{
			RestMapper: restMapper,
			Client:     dynamicClient,
			Scheme:     scheme,
		},
		EndpointNamespace: namespace,
		ClusterID:         t.engine.LocalEndPoint.Spec.ClusterID,
		NewPinger: func(pingerCfg healthchecker.PingerConfig) healthchecker.PingerInterface {
			defer GinkgoRecover()
			Expect(pingerCfg.IP).To(Equal(t.pinger.GetIP()))
			return t.pinger
		},
	})

	t.endpoints = dynamicClient.Resource(*test.GetGroupVersionResourceFor(restMapper, &submarinerv1.Endpoint{})).Namespace(namespace)

	t.syncer = syncer.NewGatewaySyncer(t.engine, t.gateways, t.expectedGateway.Status.Version, t.healthChecker)

	informerFactory := submarinerInformers.NewSharedInformerFactory(t.client, 0)
	informer := informerFactory.Submariner().V1().Gateways().Informer()

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			t.gatewayUpdated <- obj.(*submarinerv1.Gateway)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			t.gatewayUpdated <- newObj.(*submarinerv1.Gateway)
		},
		DeleteFunc: func(obj interface{}) {
			t.gatewayDeleted <- obj.(*submarinerv1.Gateway)
		},
	})

	go informer.Run(t.stopInformer)
	Expect(cache.WaitForCacheSync(t.stopInformer, informer.HasSynced)).To(BeTrue())

	t.syncer.Run(t.stopSyncer)

	Expect(t.healthChecker.Start(t.stopSyncer)).To(Succeed())
}

func (t *testDriver) stop() {
	close(t.stopSyncer)

	if t.expectedDeletedAfter != nil {
		t.awaitGatewayDeleted(t.expectedDeletedAfter)
	}

	close(t.stopInformer)
	utilruntime.ErrorHandlers = t.savedErrorHandlers
}

func (t *testDriver) awaitGatewayUpdated(expected *submarinerv1.Gateway) {
	t.awaitGateway(t.gatewayUpdated, expected)
}

func (t *testDriver) awaitNoGatewayUpdated() {
	Consistently(t.gatewayUpdated, syncer.GatewayUpdateInterval+50).ShouldNot(Receive(), "Gateway was unexpectedly received")
}

func (t *testDriver) awaitGatewayDeleted(expected *submarinerv1.Gateway) {
	t.awaitGateway(t.gatewayDeleted, expected)
}

func (t *testDriver) awaitNoGatewayDeleted() {
	Consistently(t.gatewayDeleted, syncer.GatewayUpdateInterval+50).ShouldNot(Receive(), "Gateway was unexpectedly deleted")
}

func (t *testDriver) awaitGateway(gatewayChan chan *submarinerv1.Gateway, expected *submarinerv1.Gateway) {
	var last *submarinerv1.Gateway

	Eventually(func() *submarinerv1.Gateway {
		select {
		case gw := <-gatewayChan:
			last = gw
			return gw
		default:
			return last
		}
	}, 5).Should(equalGateway(expected))
}

func TestSyncer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cable engine syncer Suite")
}

type equalGatewayMatcher struct {
	expected *submarinerv1.Gateway
}

func equalGateway(expected *submarinerv1.Gateway) gomegaTypes.GomegaMatcher {
	return &equalGatewayMatcher{expected}
}

func (m *equalGatewayMatcher) Match(x interface{}) (bool, error) {
	actual := x.(*submarinerv1.Gateway)
	if actual == nil {
		return false, nil
	}

	if actual.Name != m.expected.Name {
		return false, nil
	}

	if m.expected.Status.StatusFailure != "" {
		if !strings.Contains(actual.Status.StatusFailure, m.expected.Status.StatusFailure) {
			return false, nil
		}

		actual = actual.DeepCopy()
		actual.Status.StatusFailure = m.expected.Status.StatusFailure
	}

	return reflect.DeepEqual(actual.Status, m.expected.Status), nil
}

func (m *equalGatewayMatcher) FailureMessage(actual interface{}) string {
	return format.Message(actual, "to equal", m.expected)
}

func (m *equalGatewayMatcher) NegatedFailureMessage(actual interface{}) (message string) {
	return format.Message(actual, "not to equal", m.expected)
}
