package syncer_test

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	. "github.com/submariner-io/admiral/pkg/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	fakeEngine "github.com/submariner-io/submariner/pkg/cableengine/fake"
	"github.com/submariner-io/submariner/pkg/cableengine/healthchecker"
	"github.com/submariner-io/submariner/pkg/cableengine/syncer"
	fakeClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned/fake"
	fakeClientsetv1 "github.com/submariner-io/submariner/pkg/client/clientset/versioned/typed/submariner.io/v1/fake"
	submarinerInformers "github.com/submariner-io/submariner/pkg/client/informers/externalversions"
	"github.com/submariner-io/submariner/pkg/types"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	fakeClient "k8s.io/client-go/dynamic/fake"
	kubeScheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

const (
	namespace = "submariner"
)

var _ = Describe("", func() {
	klog.InitFlags(nil)
})

var _ = BeforeSuite(func() {
	syncer.GatewayUpdateInterval = 200 * time.Millisecond
	syncer.GatewayStaleTimeout = 1 * time.Second
})

var _ = Describe("", func() {
	Context("Gateway syncing", testGatewaySyncing)
	Context("Stale Gateway cleanup", testStaleGatewayCleanup)
	Context("Gateway sync errors", testGatewaySyncErrors)
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

	BeforeEach(func() {
		t = newTestDriver()
		staleGateway = &submarinerv1.Gateway{
			ObjectMeta: v1.ObjectMeta{
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

		_, err := t.gateways.Create(staleGateway)
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
		BeforeEach(func() {
			t.gateways.FailOnList = errors.New("fake error")
		})

		It("should log the error", func() {
			Eventually(t.handledError, 5).Should(Receive(ContainErrorSubstring(t.gateways.FailOnList)))
		})
	})

	When("Gateway delete fails", func() {
		BeforeEach(func() {
			t.gateways.FailOnDelete = errors.New("fake error")
			t.expectedDeletedAfter = nil
		})

		It("should log the error", func() {
			Eventually(t.handledError, 5).Should(Receive(ContainErrorSubstring(t.gateways.FailOnDelete)))
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
			t.gateways.FailOnCreate = expectedErr
		})

		It("should log the error", func() {
			Eventually(t.handledError, 5).Should(Receive(ContainErrorSubstring(expectedErr)))
		})
	})

	When("Gateway update fails", func() {
		BeforeEach(func() {
			t.gateways.FailOnUpdate = expectedErr
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
			t.gateways.FailOnGet = expectedErr
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

type testDriver struct {
	engine               *fakeEngine.Engine
	gateways             *fakeClientsetv1.FailingGateways
	syncer               *syncer.GatewaySyncer
	healthChecker        healthchecker.Interface
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
	t := &testDriver{
		engine:             fakeEngine.New(),
		gateways:           &fakeClientsetv1.FailingGateways{},
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
		ObjectMeta: v1.ObjectMeta{
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

	client := fakeClientset.NewSimpleClientset()
	t.gateways.GatewayInterface = client.SubmarinerV1().Gateways(namespace)
	t.healthChecker, _ = healthchecker.New(&watcher.Config{
		RestMapper: restMapper,
		Client:     dynamicClient,
		Scheme:     scheme,
	}, namespace, "west")
	t.syncer = syncer.NewGatewaySyncer(t.engine, t.gateways, t.expectedGateway.Status.Version, t.healthChecker)

	informerFactory := submarinerInformers.NewSharedInformerFactory(client, 0)
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
	t.awaitGateway(t.gatewayUpdated, fmt.Sprintf("Gateway was not received - %#v", expected), expected)
}

func (t *testDriver) awaitNoGatewayUpdated() {
	Consistently(t.gatewayUpdated, syncer.GatewayUpdateInterval+50).ShouldNot(Receive(), "Gateway was unexpectedly received")
}

func (t *testDriver) awaitGatewayDeleted(expected *submarinerv1.Gateway) {
	t.awaitGateway(t.gatewayDeleted, fmt.Sprintf("Gateway was not deleted - %#v", expected), expected)
}

func (t *testDriver) awaitNoGatewayDeleted() {
	Consistently(t.gatewayDeleted, syncer.GatewayUpdateInterval+50).ShouldNot(Receive(), "Gateway was unexpectedly deleted")
}

func (t *testDriver) awaitGateway(gatewayChan chan *submarinerv1.Gateway, msg string, expected *submarinerv1.Gateway) {
	actual, err := func() (*submarinerv1.Gateway, error) {
		select {
		case gw := <-gatewayChan:
			return gw, nil
		case <-time.After(5 * time.Second):
			return nil, fmt.Errorf(msg)
		}
	}()

	Expect(err).To(Succeed())
	Expect(actual.Name).To(Equal(expected.Name))

	if expected.Status.StatusFailure != "" {
		Expect(actual.Status.StatusFailure).To(ContainSubstring(expected.Status.StatusFailure))
		actual = actual.DeepCopy()
		actual.Status.StatusFailure = expected.Status.StatusFailure
	}

	Expect(actual.Status).To(Equal(expected.Status))
}

func TestSyncer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cable engine syncer Suite")
}
