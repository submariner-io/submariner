package tunnel_test

import (
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	fakeEngine "github.com/submariner-io/submariner/pkg/cableengine/fake"
	fakeClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned/fake"
	submarinerClientsetv1 "github.com/submariner-io/submariner/pkg/client/clientset/versioned/typed/submariner.io/v1"
	informers "github.com/submariner-io/submariner/pkg/client/informers/externalversions"
	"github.com/submariner-io/submariner/pkg/controllers/tunnel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
)

const (
	namespace = "submariner"
)

var _ = Describe("", func() {
	klog.InitFlags(nil)
})

var _ = Describe("Managing tunnels", func() {
	var (
		engine           *fakeEngine.Engine
		endpoints        submarinerClientsetv1.EndpointInterface
		endpoint         *v1.Endpoint
		initialEndpoints []runtime.Object
		stopCh           chan struct{}
		runCompleted     chan error
	)

	BeforeEach(func() {
		engine = fakeEngine.New()
		initialEndpoints = nil

		endpoint = &v1.Endpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "east-submariner-cable-east-192-68-1-1",
				Namespace: namespace,
			},
			Spec: v1.EndpointSpec{
				CableName: "submariner-cable-east-192-68-1-1",
				ClusterID: "east",
				Hostname:  "redsox",
				PrivateIP: "192.68.1.2",
			}}
	})

	JustBeforeEach(func() {
		stopCh = make(chan struct{})
		runCompleted = make(chan error, 1)
		clientset := fakeClientset.NewSimpleClientset(initialEndpoints...)
		endpoints = clientset.SubmarinerV1().Endpoints(namespace)

		informerFactory := informers.NewSharedInformerFactoryWithOptions(clientset, 0,
			informers.WithNamespace(namespace))

		controller := tunnel.NewController(engine, informerFactory.Submariner().V1().Endpoints())

		informerFactory.Start(stopCh)

		go func() {
			runCompleted <- controller.Run(stopCh)
		}()
	})

	AfterEach(func() {
		close(stopCh)

		err := func() error {
			timeout := 5 * time.Second
			select {
			case err := <-runCompleted:
				return errors.WithMessage(err, "Run returned an error")
			case <-time.After(timeout):
				return fmt.Errorf("Run did not complete after %v", timeout)
			}
		}()

		Expect(err).To(Succeed())
	})

	When("an Endpoint is created", func() {
		It("should install the cable", func() {
			_, err := endpoints.Create(endpoint)
			Expect(err).To(Succeed())

			engine.VerifyInstallCable(&endpoint.Spec)
		})
	})

	When("an Endpoint is updated", func() {
		BeforeEach(func() {
			initialEndpoints = append(initialEndpoints, endpoint)
		})

		It("should install the cable", func() {
			engine.VerifyInstallCable(&endpoint.Spec)

			endpoint.Spec.Subnets = []string{"100.0.0.0/16", "10.0.0.0/14"}
			_, err := endpoints.Update(endpoint)
			Expect(err).To(Succeed())

			engine.VerifyInstallCable(&endpoint.Spec)
		})
	})

	When("an Endpoint is deleted", func() {
		BeforeEach(func() {
			initialEndpoints = append(initialEndpoints, endpoint)
		})

		It("should remove the cable", func() {
			engine.VerifyInstallCable(&endpoint.Spec)

			Expect(endpoints.Delete(endpoint.Name, nil)).To(Succeed())
			engine.VerifyRemoveCable(&endpoint.Spec)
		})
	})

	When("install cable initially fails", func() {
		BeforeEach(func() {
			engine.ErrOnInstallCable = errors.New("fake error")
			initialEndpoints = append(initialEndpoints, endpoint)
		})

		It("should retry until it succeeds", func() {
			engine.VerifyInstallCable(&endpoint.Spec)
		})
	})
})

func TestTunnelController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tunnel controller Suite")
}
