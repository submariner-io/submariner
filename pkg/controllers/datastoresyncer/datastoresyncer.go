package datastoresyncer

import (
	"fmt"

	"github.com/submariner-io/admiral/pkg/federate"
	resourceSyncer "github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
)

type NewSyncer func(*broker.SyncerConfig) (*broker.Syncer, error)

type DatastoreSyncer struct {
	colorCodes    []string
	localCluster  types.SubmarinerCluster
	localEndpoint types.SubmarinerEndpoint
	syncerConfig  broker.SyncerConfig
	newSyncer     NewSyncer
}

func New(restConfig *rest.Config, namespace string, localCluster types.SubmarinerCluster,
	localEndpoint types.SubmarinerEndpoint, colorcodes []string) *DatastoreSyncer {
	return &DatastoreSyncer{
		colorCodes:    colorcodes,
		localCluster:  localCluster,
		localEndpoint: localEndpoint,
		syncerConfig: broker.SyncerConfig{
			LocalRestConfig: restConfig,
			LocalNamespace:  namespace,
			LocalClusterID:  localCluster.Spec.ClusterID,
		},
		newSyncer: func(config *broker.SyncerConfig) (*broker.Syncer, error) {
			return broker.NewSyncer(*config)
		},
	}
}

// Intended for unit tests.
func NewWithDetail(thisClusterID, namespace string, localCluster types.SubmarinerCluster,
	localEndpoint types.SubmarinerEndpoint, colorcodes []string, syncerConfig broker.SyncerConfig, newSyncer NewSyncer) *DatastoreSyncer {
	return &DatastoreSyncer{
		colorCodes:    colorcodes,
		localCluster:  localCluster,
		localEndpoint: localEndpoint,
		syncerConfig:  syncerConfig,
		newSyncer:     newSyncer,
	}
}

func (d *DatastoreSyncer) Run(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()

	klog.Info("Starting the datastore syncer")

	d.syncerConfig.ResourceConfigs = []broker.ResourceConfig{
		{
			LocalSourceNamespace: d.syncerConfig.LocalNamespace,
			LocalResourceType:    &submarinerv1.Cluster{},
			LocalTransform:       d.shouldSyncCluster,
			BrokerResourceType:   &submarinerv1.Cluster{},
		},
		{
			LocalSourceNamespace: d.syncerConfig.LocalNamespace,
			LocalResourceType:    &submarinerv1.Endpoint{},
			LocalTransform:       d.shouldSyncEndpoint,
			BrokerResourceType:   &submarinerv1.Endpoint{},
		},
	}

	syncer, err := d.newSyncer(&d.syncerConfig)
	if err != nil {
		return err
	}

	klog.Info("Starting the broker syncer")

	err = syncer.Start(stopCh)
	if err != nil {
		return err
	}

	if err := d.ensureExclusiveEndpoint(syncer); err != nil {
		return fmt.Errorf("could not ensure exclusive submariner Endpoint: %v", err)
	}

	if err := d.createLocalCluster(syncer.GetLocalFederator()); err != nil {
		return fmt.Errorf("error creating the local submariner Cluster: %v", err)
	}

	if err := d.createLocalEndpoint(syncer.GetLocalFederator()); err != nil {
		return fmt.Errorf("error creating the local submariner Endpoint: %v", err)
	}

	klog.Info("Datastore syncer started")

	<-stopCh
	klog.Info("Datastore syncer stopping")

	return nil
}

func (d *DatastoreSyncer) shouldSyncEndpoint(obj runtime.Object, op resourceSyncer.Operation) (runtime.Object, bool) {
	// Ensure we don't try to sync a remote endpoint to the broker. While the syncer handles this normally using a
	// label, on upgrade to 0.8.0 where the syncer was introduced, the label won't exist so check the ClusterID field here.
	endpoint := obj.(*submarinerv1.Endpoint)
	if endpoint.Spec.ClusterID == d.localCluster.Spec.ClusterID {
		return obj, false
	}

	return nil, false
}

func (d *DatastoreSyncer) shouldSyncCluster(obj runtime.Object, op resourceSyncer.Operation) (runtime.Object, bool) {
	cluster := obj.(*submarinerv1.Cluster)
	if cluster.Spec.ClusterID == d.localCluster.Spec.ClusterID {
		return obj, false
	}

	return nil, false
}

func (d *DatastoreSyncer) ensureExclusiveEndpoint(syncer *broker.Syncer) error {
	klog.Info("Ensuring we are the only endpoint active for this cluster")

	endpoints, err := syncer.ListLocalResources(&submarinerv1.Endpoint{})
	if err != nil {
		return fmt.Errorf("error retrieving submariner Endpoints: %v", err)
	}

	for i := range endpoints {
		endpoint := endpoints[i].(*submarinerv1.Endpoint)
		if endpoint.Spec.ClusterID != d.localCluster.Spec.ClusterID {
			continue
		}

		if util.CompareEndpointSpec(endpoint.Spec, d.localEndpoint.Spec) {
			continue
		}

		endpointName, err := util.GetEndpointCRDNameFromParams(endpoint.Spec.ClusterID, endpoint.Spec.CableName)
		if err != nil {
			klog.Errorf("Error extracting the submariner Endpoint name from %#v: %v", endpoint, err)
			continue
		}

		err = syncer.GetLocalFederator().Delete(endpoint)
		if err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("error deleting submariner Endpoint %q from the local datastore: %v", endpointName, err)
		}

		klog.Infof("Successfully deleted existing submariner Endpoint %q", endpointName)
	}

	return nil
}

func (d *DatastoreSyncer) createLocalCluster(federator federate.Federator) error {
	klog.Infof("Creating local submariner Cluster: %#v ", d.localCluster)

	cluster := &submarinerv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: d.localCluster.Spec.ClusterID,
		},
		Spec: d.localCluster.Spec,
	}

	return federator.Distribute(cluster)
}

func (d *DatastoreSyncer) createLocalEndpoint(federator federate.Federator) error {
	klog.Infof("Creating local submariner Endpoint: %#v ", d.localEndpoint)

	endpointName, err := util.GetEndpointCRDName(&d.localEndpoint)
	if err != nil {
		return fmt.Errorf("error extracting the submariner Endpoint name from %#v: %v", d.localEndpoint, err)
	}

	endpoint := &submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: endpointName,
		},
		Spec: d.localEndpoint.Spec,
	}

	return federator.Distribute(endpoint)
}
