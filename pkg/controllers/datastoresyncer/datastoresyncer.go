/*
© 2021 Red Hat, Inc. and others

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
package datastoresyncer

import (
	"fmt"
	"os"

	"github.com/submariner-io/admiral/pkg/federate"
	resourceSyncer "github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/watcher"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog"

	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/types"
	"github.com/submariner-io/submariner/pkg/util"
)

type DatastoreSyncer struct {
	colorCodes     []string
	localCluster   types.SubmarinerCluster
	localEndpoint  types.SubmarinerEndpoint
	localNodeName  string
	syncerConfig   broker.SyncerConfig
	localFederator federate.Federator
}

func New(syncerConfig broker.SyncerConfig, localCluster types.SubmarinerCluster,
	localEndpoint types.SubmarinerEndpoint, colorcodes []string) *DatastoreSyncer {
	syncerConfig.LocalClusterID = localCluster.Spec.ClusterID

	return &DatastoreSyncer{
		colorCodes:    colorcodes,
		localCluster:  localCluster,
		localEndpoint: localEndpoint,
		syncerConfig:  syncerConfig,
	}
}

func (d *DatastoreSyncer) Start(stopCh <-chan struct{}) error {
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

	syncer, err := broker.NewSyncer(d.syncerConfig)
	if err != nil {
		return err
	}

	klog.Info("Starting the broker syncer")

	err = syncer.Start(stopCh)
	if err != nil {
		return err
	}

	d.localFederator = syncer.GetLocalFederator()

	if err := d.ensureExclusiveEndpoint(syncer); err != nil {
		return fmt.Errorf("could not ensure exclusive submariner Endpoint: %v", err)
	}

	if err := d.createLocalCluster(); err != nil {
		return fmt.Errorf("error creating the local submariner Cluster: %v", err)
	}

	if err := d.createOrUpdateLocalEndpoint(); err != nil {
		return fmt.Errorf("error creating the local submariner Endpoint: %v", err)
	}

	if len(d.localCluster.Spec.GlobalCIDR) > 0 {
		if err := d.startNodeWatcher(stopCh); err != nil {
			return fmt.Errorf("startNodeWatcher returned error: %v", err)
		}
	}

	klog.Info("Datastore syncer started")

	return nil
}

func (d *DatastoreSyncer) shouldSyncEndpoint(obj runtime.Object, numRequeues int, op resourceSyncer.Operation) (runtime.Object, bool) {
	// Ensure we don't try to sync a remote endpoint to the broker. While the syncer handles this normally using a
	// label, on upgrade to 0.8.0 where the syncer was introduced, the label won't exist so check the ClusterID field here.
	endpoint := obj.(*submarinerv1.Endpoint)
	if endpoint.Spec.ClusterID == d.localCluster.Spec.ClusterID {
		return obj, false
	}

	return nil, false
}

func (d *DatastoreSyncer) shouldSyncCluster(obj runtime.Object, numRequeues int, op resourceSyncer.Operation) (runtime.Object, bool) {
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

func (d *DatastoreSyncer) startNodeWatcher(stopCh <-chan struct{}) error {
	nodeName, ok := os.LookupEnv("NODE_NAME")
	if !ok {
		// Healthcheck in globalnet deployments will not work because of missing NODE_NAME.
		klog.Error("Error reading the NODE_NAME from the env, healthChecker functionality will not work.")
	} else {
		d.localNodeName = nodeName
		return d.createNodeWatcher(stopCh)
	}

	return nil
}

func (d *DatastoreSyncer) createNodeWatcher(stopCh <-chan struct{}) error {
	resourceWatcher, err := watcher.New(&watcher.Config{
		Scheme:     scheme.Scheme,
		RestConfig: d.syncerConfig.LocalRestConfig,
		RestMapper: d.syncerConfig.RestMapper,
		Client:     d.syncerConfig.LocalClient,
		ResourceConfigs: []watcher.ResourceConfig{
			{
				Name:                "Node watcher for datastoresyncer",
				ResourceType:        &k8sv1.Node{},
				ResourcesEquivalent: d.areNodesEquivalent,
				Handler: watcher.EventHandlerFuncs{
					OnCreateFunc: d.handleCreateOrUpdateNode,
					OnUpdateFunc: d.handleCreateOrUpdateNode,
					OnDeleteFunc: nil,
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("error creating resource watcher for Nodes %v", err)
	}

	err = resourceWatcher.Start(stopCh)
	if err != nil {
		return err
	}

	return nil
}

func (d *DatastoreSyncer) createLocalCluster() error {
	klog.Infof("Creating local submariner Cluster: %#v ", d.localCluster)

	cluster := &submarinerv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: d.localCluster.Spec.ClusterID,
		},
		Spec: d.localCluster.Spec,
	}

	return d.localFederator.Distribute(cluster)
}

func (d *DatastoreSyncer) createOrUpdateLocalEndpoint() error {
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

	return d.localFederator.Distribute(endpoint)
}
