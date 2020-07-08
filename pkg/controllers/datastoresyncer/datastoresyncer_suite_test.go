package datastoresyncer_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	. "github.com/submariner-io/admiral/pkg/gomega"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	fakeClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned/fake"
	submarinerClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned/typed/submariner.io/v1"
	fakeClientsetv1 "github.com/submariner-io/submariner/pkg/client/clientset/versioned/typed/submariner.io/v1/fake"
	informers "github.com/submariner-io/submariner/pkg/client/informers/externalversions"
	"github.com/submariner-io/submariner/pkg/controllers/datastoresyncer"
	"github.com/submariner-io/submariner/pkg/datastore"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/util"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"
)

const (
	clusterID       = "east"
	otherClusterID  = "west"
	namespace       = "submariner"
	brokerNamespace = "submariner-broker"
)

func TestDatastoresyncer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Datastore syncer Suite")
}

var _ = Describe("", func() {
	klog.InitFlags(nil)
})

type testDriver struct {
	syncer              *datastoresyncer.DatastoreSyncer
	datastore           *testDatastore
	localCluster        *types.SubmarinerCluster
	localEndpoint       *types.SubmarinerEndpoint
	submarinerClusters  *fakeClientsetv1.FailingClusters
	submarinerEndpoints *fakeClientsetv1.FailingEndpoints
	stopCh              chan struct{}
	runCompleted        chan error
	initialLocalObjs    []runtime.Object
	initialRemoteObjs   []runtime.Object
	expectedRunErr      error
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
		t.initialLocalObjs = nil
		t.initialRemoteObjs = nil
		t.datastore = &testDatastore{}
		t.submarinerClusters = &fakeClientsetv1.FailingClusters{}
		t.submarinerEndpoints = &fakeClientsetv1.FailingEndpoints{}
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

	remoteClient := fakeClientset.NewSimpleClientset(t.initialRemoteObjs...).SubmarinerV1()
	t.datastore.clusters = remoteClient.Clusters(brokerNamespace)
	t.datastore.endpoints = remoteClient.Endpoints(brokerNamespace)

	localClient := fakeClientset.NewSimpleClientset(t.initialLocalObjs...)
	t.submarinerClusters.ClusterInterface = localClient.SubmarinerV1().Clusters(namespace)
	t.submarinerEndpoints.EndpointInterface = localClient.SubmarinerV1().Endpoints(namespace)

	informerFactory := informers.NewSharedInformerFactoryWithOptions(localClient, 0,
		informers.WithNamespace(namespace))

	t.syncer = datastoresyncer.NewDatastoreSyncer(clusterID, t.submarinerClusters, informerFactory.Submariner().V1().Clusters(),
		t.submarinerEndpoints, informerFactory.Submariner().V1().Endpoints(), t.datastore, []string{}, *t.localCluster, *t.localEndpoint)

	informerFactory.Start(t.stopCh)

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
			Name:      getEndpointName(spec),
			Namespace: namespace,
		},
		Spec: *spec,
	}
}

func createEndpoint(endpoints submarinerClientset.EndpointInterface, spec *submarinerv1.EndpointSpec) string {
	endpointName := getEndpointName(spec)
	_, err := endpoints.Create(&submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name:      endpointName,
			Namespace: namespace,
		},
		Spec: *spec,
	})

	Expect(err).To(Succeed())
	return endpointName
}

func createCluster(clusters submarinerClientset.ClusterInterface, spec *submarinerv1.ClusterSpec) string {
	_, err := clusters.Create(&submarinerv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spec.ClusterID,
			Namespace: namespace,
		},
		Spec: *spec,
	})

	Expect(err).To(Succeed())
	return spec.ClusterID
}

func getEndpointName(from *submarinerv1.EndpointSpec) string {
	endpointName, err := util.GetEndpointCRDNameFromParams(from.ClusterID, from.CableName)
	Expect(err).To(Succeed())
	return endpointName
}

func awaitCluster(clusters submarinerClientset.ClusterInterface, expected *submarinerv1.ClusterSpec) {
	var foundCluster *submarinerv1.Cluster
	err := wait.PollImmediate(50*time.Millisecond, 5*time.Second, func() (bool, error) {
		cluster, err := clusters.Get(expected.ClusterID, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}

		foundCluster = cluster
		return true, nil
	})

	Expect(err).To(Succeed())
	Expect(foundCluster.Spec).To(Equal(*expected))
}

func awaitNoCluster(clusters submarinerClientset.ClusterInterface, name string) {
	err := wait.PollImmediate(50*time.Millisecond, 5*time.Second, func() (bool, error) {
		_, err := clusters.Get(name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}

		if err != nil {
			return false, err
		}

		return false, nil
	})

	Expect(err).To(Succeed())
}

func awaitEndpoint(endpoints submarinerClientset.EndpointInterface, expected *submarinerv1.EndpointSpec) {
	endpointName := getEndpointName(expected)

	var foundEndpoint *submarinerv1.Endpoint
	err := wait.PollImmediate(50*time.Millisecond, 5*time.Second, func() (bool, error) {
		endpoint, err := endpoints.Get(endpointName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}

		foundEndpoint = endpoint
		return true, nil
	})

	Expect(err).To(Succeed())
	Expect(foundEndpoint.Spec).To(Equal(*expected))
}

func awaitNoEndpoint(endpoints submarinerClientset.EndpointInterface, name string) {
	err := wait.PollImmediate(50*time.Millisecond, 5*time.Second, func() (bool, error) {
		_, err := endpoints.Get(name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}

		if err != nil {
			return false, err
		}

		return false, nil
	})

	Expect(err).To(Succeed())
}

type testDatastore struct {
	clusters             submarinerClientset.ClusterInterface
	endpoints            submarinerClientset.EndpointInterface
	failOnRemoveEndpoint error
}

func (t *testDatastore) GetEndpoints(clusterID string) ([]types.SubmarinerEndpoint, error) {
	endpoints, err := t.endpoints.List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	subEndpoints := []types.SubmarinerEndpoint{}
	for _, endpoint := range endpoints.Items {
		if endpoint.Spec.ClusterID == clusterID {
			subEndpoints = append(subEndpoints, types.SubmarinerEndpoint{Spec: endpoint.Spec})
		}
	}

	return subEndpoints, nil
}

func (t *testDatastore) SetCluster(cluster *types.SubmarinerCluster) error {
	_, err := t.clusters.Create(&submarinerv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: cluster.Spec.ClusterID,
		},
		Spec: cluster.Spec,
	})
	return err
}

func (t *testDatastore) SetEndpoint(endpoint *types.SubmarinerEndpoint) error {
	endpointName := getEndpointName(&endpoint.Spec)
	_, err := t.endpoints.Create(&submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: endpointName,
		},
		Spec: endpoint.Spec,
	})
	return err
}

func (t *testDatastore) RemoveEndpoint(clusterID, cableName string) error {
	if t.failOnRemoveEndpoint != nil {
		return t.failOnRemoveEndpoint
	}

	endpointName, err := util.GetEndpointCRDNameFromParams(clusterID, cableName)
	if err != nil {
		return err
	}

	return t.endpoints.Delete(endpointName, &metav1.DeleteOptions{})
}

func (t *testDatastore) WatchClusters(ctx context.Context, selfClusterID string, colorCodes []string,
	onClusterChange datastore.OnClusterChange) error {
	return nil
}

func (t *testDatastore) WatchEndpoints(ctx context.Context, selfClusterID string, colorCodes []string,
	onEndpointChange datastore.OnEndpointChange) error {
	return nil
}
