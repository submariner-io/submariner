package tunnel_test

import (
	"errors"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	fakeEngine "github.com/submariner-io/submariner/pkg/cableengine/fake"
	"github.com/submariner-io/submariner/pkg/controllers/tunnel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	fakeClient "k8s.io/client-go/dynamic/fake"
	kubeScheme "k8s.io/client-go/kubernetes/scheme"
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
		engine    *fakeEngine.Engine
		endpoints dynamic.ResourceInterface
		endpoint  *v1.Endpoint
		stopCh    chan struct{}
	)

	BeforeEach(func() {
		engine = fakeEngine.New()

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

		Expect(v1.AddToScheme(kubeScheme.Scheme)).To(Succeed())

		scheme := runtime.NewScheme()
		Expect(v1.AddToScheme(scheme)).To(Succeed())

		client := fakeClient.NewSimpleDynamicClient(scheme)

		restMapper, gvr := test.GetRESTMapperAndGroupVersionResourceFor(&v1.Endpoint{})

		endpoints = client.Resource(*gvr).Namespace(namespace)

		Expect(tunnel.StartController(engine, namespace, client, restMapper, scheme, stopCh)).To(Succeed())
	})

	AfterEach(func() {
		close(stopCh)
	})

	When("an Endpoint is created", func() {
		It("should install the cable", func() {
			test.CreateResource(endpoints, endpoint)
			engine.VerifyInstallCable(&endpoint.Spec)
		})
	})

	When("an Endpoint is updated", func() {
		It("should install the cable", func() {
			test.CreateResource(endpoints, endpoint)
			engine.VerifyInstallCable(&endpoint.Spec)

			endpoint.Spec.Subnets = []string{"100.0.0.0/16", "10.0.0.0/14"}
			test.UpdateResource(endpoints, endpoint)

			engine.VerifyInstallCable(&endpoint.Spec)
		})
	})

	When("an Endpoint is deleted", func() {
		It("should remove the cable", func() {
			test.CreateResource(endpoints, endpoint)
			engine.VerifyInstallCable(&endpoint.Spec)

			Expect(endpoints.Delete(endpoint.Name, nil)).To(Succeed())
			engine.VerifyRemoveCable(&endpoint.Spec)
		})
	})

	When("install cable initially fails", func() {
		BeforeEach(func() {
			engine.ErrOnInstallCable = errors.New("fake error")
		})

		It("should retry until it succeeds", func() {
			test.CreateResource(endpoints, endpoint)
			engine.VerifyInstallCable(&endpoint.Spec)
		})
	})

	When("remove cable initially fails", func() {
		BeforeEach(func() {
			engine.ErrOnRemoveCable = errors.New("fake error")
		})

		It("should retry until it succeeds", func() {
			test.CreateResource(endpoints, endpoint)
			engine.VerifyInstallCable(&endpoint.Spec)

			Expect(endpoints.Delete(endpoint.Name, nil)).To(Succeed())
			engine.VerifyRemoveCable(&endpoint.Spec)
		})
	})
})

func TestTunnelController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tunnel controller Suite")
}
