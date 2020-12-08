package datastoresyncer_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/fake"
	. "github.com/submariner-io/admiral/pkg/gomega"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/syncer/test"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/controllers/datastoresyncer"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/util"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog"
)

const (
	clusterID       = "east"
	otherClusterID  = "west"
	localNamespace  = "submariner"
	brokerNamespace = "submariner-broker"
)

func TestDatastoresyncer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Datastore syncer Suite")
}

func init() {
	klog.InitFlags(nil)

	err := submarinerv1.AddToScheme(scheme.Scheme)
	if err != nil {
		panic(err)
	}
}

type testDriver struct {
	syncer          *datastoresyncer.DatastoreSyncer
	localCluster    *types.SubmarinerCluster
	localEndpoint   *types.SubmarinerEndpoint
	localClient     dynamic.Interface
	brokerClient    dynamic.Interface
	localClusters   *fake.DynamicResourceClient
	brokerClusters  dynamic.ResourceInterface
	localEndpoints  *fake.DynamicResourceClient
	brokerEndpoints dynamic.ResourceInterface
	syncerScheme    *runtime.Scheme
	restMapper      meta.RESTMapper
	stopCh          chan struct{}
	runCompleted    chan error
	expectedRunErr  error
}

func newTestDriver() *testDriver {
	t := &testDriver{
		localCluster: &types.SubmarinerCluster{
			ID: clusterID,
			Spec: submarinerv1.ClusterSpec{
				ClusterID:   clusterID,
				ServiceCIDR: []string{"100.0.0.0/16"},
				ClusterCIDR: []string{"10.0.0.0/14"},
			},
		},
		localEndpoint: &types.SubmarinerEndpoint{
			Spec: submarinerv1.EndpointSpec{
				CableName: fmt.Sprintf("submariner-cable-%s-192-68-1-2", clusterID),
				ClusterID: clusterID,
				Hostname:  "redsox",
				PrivateIP: "192.68.1.2",
				Subnets:   []string{"100.0.0.0/16", "10.0.0.0/14"},
				Backend:   "ipsec",
			},
		},
	}

	BeforeEach(func() {
		t.expectedRunErr = nil

		t.syncerScheme = runtime.NewScheme()
		Expect(submarinerv1.AddToScheme(t.syncerScheme)).To(Succeed())

		t.localClient = fake.NewDynamicClient(t.syncerScheme)
		t.brokerClient = fake.NewDynamicClient(t.syncerScheme)

		t.restMapper = test.GetRESTMapperFor(&submarinerv1.Cluster{}, &submarinerv1.Endpoint{})

		clusterGVR := test.GetGroupVersionResourceFor(t.restMapper, &submarinerv1.Cluster{})
		t.localClusters = t.localClient.Resource(*clusterGVR).Namespace(localNamespace).(*fake.DynamicResourceClient)
		t.brokerClusters = t.brokerClient.Resource(*clusterGVR).Namespace(brokerNamespace)

		endpointGVR := test.GetGroupVersionResourceFor(t.restMapper, &submarinerv1.Endpoint{})
		t.localEndpoints = t.localClient.Resource(*endpointGVR).Namespace(localNamespace).(*fake.DynamicResourceClient)
		t.brokerEndpoints = t.brokerClient.Resource(*endpointGVR).Namespace(brokerNamespace)
	})

	JustBeforeEach(func() {
		t.run()
	})

	AfterEach(func() {
		t.stop()
	})

	return t
}

func (t *testDriver) run() {
	t.stopCh = make(chan struct{})
	t.runCompleted = make(chan error, 1)

	t.syncer = datastoresyncer.NewWithDetail(clusterID, localNamespace, *t.localCluster, *t.localEndpoint, []string{},
		broker.SyncerConfig{
			LocalNamespace:  localNamespace,
			LocalClusterID:  clusterID,
			BrokerNamespace: brokerNamespace,
			Scheme:          t.syncerScheme,
		}, func(config *broker.SyncerConfig) (*broker.Syncer, error) {
			return broker.NewSyncerWithDetail(config, t.localClient, t.brokerClient, t.restMapper)
		})

	go func() {
		t.runCompleted <- t.syncer.Run(t.stopCh)
	}()
}

func (t *testDriver) stop() {
	if t.stopCh == nil {
		return
	}

	if t.expectedRunErr == nil {
		close(t.stopCh)
	}

	err := func() error {
		timeout := 5 * time.Second
		select {
		case err := <-t.runCompleted:
			return errors.WithMessage(err, "Run returned an error")
		case <-time.After(timeout):
			return fmt.Errorf("Run did not complete after %v", timeout)
		}
	}()

	t.stopCh = nil
	if t.expectedRunErr == nil {
		Expect(err).To(Succeed())
	} else {
		Expect(err).To(ContainErrorSubstring(t.expectedRunErr))
	}
}

func newEndpoint(spec *submarinerv1.EndpointSpec) *submarinerv1.Endpoint {
	return &submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: getEndpointName(spec),
		},
		Spec: *spec,
	}
}

func newCluster(spec *submarinerv1.ClusterSpec) *submarinerv1.Cluster {
	return &submarinerv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: spec.ClusterID,
		},
		Spec: *spec,
	}
}

func getEndpointName(from *submarinerv1.EndpointSpec) string {
	endpointName, err := util.GetEndpointCRDNameFromParams(from.ClusterID, from.CableName)
	Expect(err).To(Succeed())

	return endpointName
}

func awaitCluster(clusters dynamic.ResourceInterface, expected *submarinerv1.ClusterSpec) {
	test.AwaitAndVerifyResource(clusters, expected.ClusterID, func(obj *unstructured.Unstructured) bool {
		defer GinkgoRecover()
		actual := &submarinerv1.Cluster{}
		Expect(scheme.Scheme.Convert(obj, actual, nil)).To(Succeed())
		return reflect.DeepEqual(actual.Spec, *expected)
	})
}

func awaitEndpoint(endpoints dynamic.ResourceInterface, expected *submarinerv1.EndpointSpec) {
	test.AwaitAndVerifyResource(endpoints, getEndpointName(expected), func(obj *unstructured.Unstructured) bool {
		defer GinkgoRecover()
		actual := &submarinerv1.Endpoint{}
		Expect(scheme.Scheme.Convert(obj, actual, nil)).To(Succeed())
		return reflect.DeepEqual(actual.Spec, *expected)
	})
}
