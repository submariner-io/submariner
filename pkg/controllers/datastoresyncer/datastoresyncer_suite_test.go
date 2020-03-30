package datastoresyncer_test

import (
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	fakeClientset "github.com/submariner-io/submariner/pkg/client/clientset/versioned/fake"
	clientsetv1 "github.com/submariner-io/submariner/pkg/client/clientset/versioned/typed/submariner.io/v1"
	fakeClientsetv1 "github.com/submariner-io/submariner/pkg/client/clientset/versioned/typed/submariner.io/v1/fake"
	informers "github.com/submariner-io/submariner/pkg/client/informers/externalversions"
	"github.com/submariner-io/submariner/pkg/controllers/datastoresyncer"
	fakeDatastore "github.com/submariner-io/submariner/pkg/datastore/fake"
	. "github.com/submariner-io/submariner/pkg/gomega"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog"
)

const (
	clusterID      = "east"
	otherClusterID = "west"
	namespace      = "submariner"
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
	mockDatastore       *fakeDatastore.Datastore
	localCluster        *types.SubmarinerCluster
	localEndpoint       *types.SubmarinerEndpoint
	submarinerClusters  *fakeClientsetv1.FailingClusters
	submarinerEndpoints *fakeClientsetv1.FailingEndpoints
	stopCh              chan struct{}
	runCompleted        chan error
	handledError        chan error
	savedErrorHandlers  []func(error)
	initialObjs         []runtime.Object
}

func newTestDriver() *testDriver {
	return &testDriver{
		mockDatastore:       fakeDatastore.New(),
		localCluster:        newSubmarinerCluster(clusterID),
		localEndpoint:       newSubmarinerEndpoint(clusterID),
		stopCh:              make(chan struct{}),
		runCompleted:        make(chan error, 1),
		handledError:        make(chan error, 1000),
		submarinerClusters:  &fakeClientsetv1.FailingClusters{},
		submarinerEndpoints: &fakeClientsetv1.FailingEndpoints{},
	}
}

func (t *testDriver) run() {
	clientset := fakeClientset.NewSimpleClientset(t.initialObjs...)
	t.submarinerClusters.ClusterInterface = clientset.SubmarinerV1().Clusters(namespace)
	t.submarinerEndpoints.EndpointInterface = clientset.SubmarinerV1().Endpoints(namespace)

	informerFactory := informers.NewSharedInformerFactoryWithOptions(clientset, 0,
		informers.WithNamespace(namespace))

	t.syncer = datastoresyncer.NewDatastoreSyncer(clusterID, t.submarinerClusters, informerFactory.Submariner().V1().Clusters(),
		t.submarinerEndpoints, informerFactory.Submariner().V1().Endpoints(), t.mockDatastore, []string{}, *t.localCluster, *t.localEndpoint)

	informerFactory.Start(t.stopCh)

	t.savedErrorHandlers = utilruntime.ErrorHandlers
	utilruntime.ErrorHandlers = append(utilruntime.ErrorHandlers, func(err error) {
		t.handledError <- err
	})

	go func() {
		t.runCompleted <- t.syncer.Run(t.stopCh)
	}()
}

func (t *testDriver) stop(expectedErr error) {
	if t.stopCh == nil {
		return
	}

	if expectedErr == nil {
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
	utilruntime.ErrorHandlers = t.savedErrorHandlers
	if expectedErr == nil {
		Expect(err).To(Succeed())
	} else {
		Expect(err).To(ContainErrorSubstring(expectedErr))
	}
}

func newEndpoint(clusterID string) *submarinerv1.Endpoint {
	return &submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterID + "-cable",
		},
		Spec: submarinerv1.EndpointSpec{
			CableName: "submariner-cable-" + clusterID,
			ClusterID: clusterID,
		},
	}
}

func newSubmarinerCluster(clusterID string) *types.SubmarinerCluster {
	return &types.SubmarinerCluster{
		ID: clusterID,
		Spec: submarinerv1.ClusterSpec{
			ClusterID:   clusterID,
			ServiceCIDR: []string{"100.0.0.0/16"},
			ClusterCIDR: []string{"10.0.0.0/14"},
		},
	}
}

func newSubmarinerEndpoint(clusterID string) *types.SubmarinerEndpoint {
	return &types.SubmarinerEndpoint{
		Spec: submarinerv1.EndpointSpec{
			CableName: fmt.Sprintf("submariner-cable-%s-192-68-1-2", clusterID),
			ClusterID: clusterID,
			Hostname:  "redsox",
			PrivateIP: "192.68.1.2",
			Subnets:   []string{"100.0.0.0/16", "10.0.0.0/14"},
			Backend:   "ipsec",
		},
	}
}

func createEndpoint(from *types.SubmarinerEndpoint, endpoints clientsetv1.EndpointInterface) string {
	endpointName := getEndpointName(from)

	_, err := endpoints.Create(&submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name:      endpointName,
			Namespace: namespace,
		},
		Spec: from.Spec,
	})

	Expect(err).To(Succeed())
	return endpointName
}

func createCluster(from *types.SubmarinerCluster, clusters clientsetv1.ClusterInterface) string {
	clusterName := getClusterName(from)

	_, err := clusters.Create(&submarinerv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: namespace,
		},
		Spec: from.Spec,
	})

	Expect(err).To(Succeed())
	return clusterName
}

func getEndpointName(from *types.SubmarinerEndpoint) string {
	endpointName, err := util.GetEndpointCRDName(from)
	Expect(err).To(Succeed())
	return endpointName
}

func getClusterName(from *types.SubmarinerCluster) string {
	clusterName, err := util.GetClusterCRDName(from)
	Expect(err).To(Succeed())
	return clusterName
}
