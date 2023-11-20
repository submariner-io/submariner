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

package datastoresyncer

import (
	"context"
	"os"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/resource"
	resourceSyncer "github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/util"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cidr"
	"github.com/submariner-io/submariner/pkg/types"
	k8sv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type DatastoreSyncer struct {
	localCluster    types.SubmarinerCluster
	localEndpoint   types.SubmarinerEndpoint
	localNodeName   string
	syncerConfig    broker.SyncerConfig
	updateFederator federate.Federator
}

var logger = log.Logger{Logger: logf.Log.WithName("DSSyncer")}

func New(syncerConfig *broker.SyncerConfig, localCluster *types.SubmarinerCluster,
	localEndpoint *types.SubmarinerEndpoint,
) *DatastoreSyncer {
	// We'll panic if syncerConfig, localCluster or localEndpoint are nil, this is intentional
	syncerConfig.LocalClusterID = localCluster.Spec.ClusterID

	return &DatastoreSyncer{
		localCluster:  *localCluster,
		localEndpoint: *localEndpoint,
		syncerConfig:  *syncerConfig,
	}
}

func (d *DatastoreSyncer) Start(ctx context.Context) error {
	defer utilruntime.HandleCrash()

	logger.Info("Starting the datastore syncer")

	d.updateFederator = federate.NewUpdateFederator(d.syncerConfig.LocalClient, d.syncerConfig.RestMapper, d.syncerConfig.LocalNamespace,
		util.CopyImmutableMetadata)

	syncer, err := d.createSyncer()
	if err != nil {
		return err
	}

	err = syncer.Start(ctx.Done())
	if err != nil {
		return errors.WithMessage(err, "error starting the syncer")
	}

	if err := d.ensureExclusiveEndpoint(ctx, syncer); err != nil {
		return errors.WithMessage(err, "could not ensure exclusive submariner Endpoint")
	}

	if err := d.createLocalCluster(ctx, syncer.GetLocalFederator()); err != nil {
		return errors.WithMessage(err, "error creating the local submariner Cluster")
	}

	if err := d.createOrUpdateLocalEndpoint(ctx, syncer.GetLocalFederator()); err != nil {
		return errors.WithMessage(err, "error creating the local submariner Endpoint")
	}

	if len(d.localCluster.Spec.GlobalCIDR) > 0 {
		if err := d.startNodeWatcher(ctx.Done()); err != nil {
			return errors.WithMessage(err, "startNodeWatcher returned error")
		}
	}

	logger.Info("Datastore syncer started")

	return nil
}

func (d *DatastoreSyncer) Cleanup(ctx context.Context) error {
	syncer, err := d.createSyncer()
	if err != nil {
		return err
	}

	localClient := d.syncerConfig.LocalClient
	if localClient == nil {
		localClient, err = dynamic.NewForConfig(d.syncerConfig.LocalRestConfig)
		if err != nil {
			return errors.Wrap(err, "error creating dynamic client")
		}
	}

	err = d.cleanupResources(ctx, localClient.Resource(schema.GroupVersionResource{
		Group:    submarinerv1.SchemeGroupVersion.Group,
		Version:  submarinerv1.SchemeGroupVersion.Version,
		Resource: "endpoints",
	}), syncer)
	if err != nil {
		return err
	}

	err = d.cleanupResources(ctx, localClient.Resource(schema.GroupVersionResource{
		Group:    submarinerv1.SchemeGroupVersion.Group,
		Version:  submarinerv1.SchemeGroupVersion.Version,
		Resource: "clusters",
	}), syncer)
	if err != nil {
		return err
	}

	return nil
}

func (d *DatastoreSyncer) cleanupResources(ctx context.Context, client dynamic.NamespaceableResourceInterface,
	syncer *broker.Syncer,
) error {
	list, err := client.Namespace(d.syncerConfig.LocalNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "error retrieving submariner resources")
	}

	for i := range list.Items {
		obj := &list.Items[i]

		err = syncer.GetLocalFederator().Delete(ctx, obj)
		if err != nil && !apierrors.IsNotFound(err) {
			return errors.Wrapf(err, "error deleting submariner %s %q from the local datastore", obj.GetKind(), obj.GetName())
		}

		logger.Infof("Successfully deleted submariner %s %q from the local datastore", obj.GetKind(), obj.GetName())

		clusterID, _, _ := unstructured.NestedString(obj.Object, "spec", "cluster_id")
		if clusterID != d.localCluster.Spec.ClusterID {
			continue
		}

		err = syncer.GetBrokerFederator().Delete(ctx, obj)
		if err != nil && !apierrors.IsNotFound(err) {
			return errors.Wrapf(err, "error deleting submariner %s %q from the remote datastore", obj.GetKind(), obj.GetName())
		}

		logger.Infof("Successfully deleted local submariner %s %q from the remote datastore", obj.GetKind(), obj.GetName())
	}

	return nil
}

func (d *DatastoreSyncer) createSyncer() (*broker.Syncer, error) {
	d.syncerConfig.ResourceConfigs = []broker.ResourceConfig{
		{
			LocalSourceNamespace: d.syncerConfig.LocalNamespace,
			LocalResourceType:    &submarinerv1.Cluster{},
			BrokerResourceType:   &submarinerv1.Cluster{},
		},
		{
			LocalSourceNamespace:   d.syncerConfig.LocalNamespace,
			LocalResourceType:      &submarinerv1.Endpoint{},
			TransformBrokerToLocal: d.shouldSyncRemoteEndpoint,
			BrokerResourceType:     &submarinerv1.Endpoint{},
		},
	}

	syncer, err := broker.NewSyncer(d.syncerConfig)

	return syncer, errors.Wrap(err, "error creating the syncer")
}

func (d *DatastoreSyncer) shouldSyncRemoteEndpoint(obj runtime.Object, _ int,
	_ resourceSyncer.Operation,
) (runtime.Object, bool) {
	endpoint := obj.(*submarinerv1.Endpoint)

	for _, localSubnet := range d.localEndpoint.Spec.Subnets {
		overlap, err := cidr.IsOverlapping(endpoint.Spec.Subnets, localSubnet)
		if err != nil {
			logger.Errorf(err, "Unable to validate if remote CIDR overlaps with local CIDR")
			return nil, false
		}

		if overlap {
			logger.Errorf(nil, "Skip processing the remote endpoint %#v as subnets are overlapping", endpoint)
			return nil, false
		}
	}

	return obj, false
}

func (d *DatastoreSyncer) ensureExclusiveEndpoint(ctx context.Context, syncer *broker.Syncer) error {
	logger.Info("Ensuring we are the only endpoint active for this cluster")

	endpoints := syncer.ListLocalResources(&submarinerv1.Endpoint{})
	for i := range endpoints {
		endpoint := endpoints[i].(*submarinerv1.Endpoint)
		if endpoint.Spec.ClusterID != d.localCluster.Spec.ClusterID {
			continue
		}

		if endpoint.Spec.Equals(&d.localEndpoint.Spec) {
			continue
		}

		endpointName, err := endpoint.Spec.GenerateName()
		if err != nil {
			logger.Errorf(err, "Error extracting the submariner Endpoint name from %#v", endpoint)
			continue
		}

		err = syncer.GetLocalFederator().Delete(ctx, endpoint)
		if err != nil && !apierrors.IsNotFound(err) {
			return errors.Wrapf(err, "error deleting submariner Endpoint %q from the local datastore", endpointName)
		}

		logger.Infof("Successfully deleted existing submariner Endpoint %q", endpointName)
	}

	return nil
}

func (d *DatastoreSyncer) startNodeWatcher(stopCh <-chan struct{}) error {
	nodeName, ok := os.LookupEnv("NODE_NAME")
	if !ok {
		// Healthcheck in globalnet deployments will not work because of missing NODE_NAME.
		logger.Error(nil, "Error reading the NODE_NAME from the env, healthChecker functionality will not work.")
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
		return errors.Wrap(err, "error creating resource watcher for Nodes")
	}

	err = resourceWatcher.Start(stopCh)
	if err != nil {
		return errors.Wrap(err, "error starting the resource watcher")
	}

	return nil
}

func (d *DatastoreSyncer) createLocalCluster(ctx context.Context, federator federate.Federator) error {
	logger.Infof("Creating local submariner Cluster: %#v ", d.localCluster)

	cluster := &submarinerv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: resource.EnsureValidName(d.localCluster.Spec.ClusterID),
		},
		Spec: d.localCluster.Spec,
	}

	return federator.Distribute(ctx, cluster) //nolint:wrapcheck  // Let the caller wrap it
}

func (d *DatastoreSyncer) createOrUpdateLocalEndpoint(ctx context.Context, federator federate.Federator) error {
	logger.Infof("Creating local submariner Endpoint: %#v ", d.localEndpoint)

	endpointName, err := d.localEndpoint.Spec.GenerateName()
	if err != nil {
		return errors.Wrapf(err, "error extracting the submariner Endpoint name from %#v", d.localEndpoint)
	}

	endpoint := &submarinerv1.Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: endpointName,
		},
		Spec: d.localEndpoint.Spec,
	}

	return federator.Distribute(ctx, endpoint) //nolint:wrapcheck  // Let the caller wrap it
}
