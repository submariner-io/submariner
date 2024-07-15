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

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/resource"
	resourceSyncer "github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/watcher"
	submarinerv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cidr"
	"github.com/submariner-io/submariner/pkg/endpoint"
	"github.com/submariner-io/submariner/pkg/types"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type DatastoreSyncer struct {
	localCluster  types.SubmarinerCluster
	localEndpoint *endpoint.Local
	syncerConfig  broker.SyncerConfig
}

var logger = log.Logger{Logger: logf.Log.WithName("DSSyncer")}

func New(syncerConfig *broker.SyncerConfig, localCluster *types.SubmarinerCluster,
	localEndpoint *endpoint.Local,
) *DatastoreSyncer {
	// We'll panic if syncerConfig, localCluster or localEndpoint are nil, this is intentional
	syncerConfig.LocalClusterID = localCluster.Spec.ClusterID

	return &DatastoreSyncer{
		localCluster:  *localCluster,
		localEndpoint: localEndpoint,
		syncerConfig:  *syncerConfig,
	}
}

func (d *DatastoreSyncer) Start(ctx context.Context) error {
	defer utilruntime.HandleCrash()

	logger.Info("Starting the datastore syncer")

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
		if err := d.startGatewayWatcher(ctx.Done()); err != nil {
			return errors.WithMessage(err, "startGatewayWatcher returned error")
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

	err = d.cleanupResources(ctx, localClient.Resource(submarinerv1.EndpointGVR), syncer)
	if err != nil {
		return err
	}

	err = d.cleanupResources(ctx, localClient.Resource(submarinerv1.ClusterGVR), syncer)
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
	remoteEndpoint := obj.(*submarinerv1.Endpoint)

	for _, localSubnet := range d.localEndpoint.Spec().Subnets {
		overlap, err := cidr.IsOverlapping(remoteEndpoint.Spec.Subnets, localSubnet)
		if err != nil {
			logger.Errorf(err, "Unable to validate if remote CIDR overlaps with local CIDR")
			return nil, false
		}

		if overlap {
			logger.Errorf(nil, "Skip processing the remote endpoint %#v as subnets are overlapping", remoteEndpoint)
			return nil, false
		}
	}

	return obj, false
}

func (d *DatastoreSyncer) ensureExclusiveEndpoint(ctx context.Context, syncer *broker.Syncer) error {
	logger.Info("Ensuring we are the only endpoint active for this cluster")

	endpoints := syncer.ListLocalResources(&submarinerv1.Endpoint{})
	for i := range endpoints {
		existing := endpoints[i].(*submarinerv1.Endpoint)
		if existing.Spec.ClusterID != d.localCluster.Spec.ClusterID {
			continue
		}

		if existing.Spec.Equals(d.localEndpoint.Spec()) && existing.Spec.Backend != "wireguard" {
			continue
		}

		err := syncer.GetLocalFederator().Delete(ctx, existing)
		if err != nil && !apierrors.IsNotFound(err) {
			return errors.Wrapf(err, "error deleting submariner Endpoint %q from the local datastore", existing.Name)
		}

		logger.Infof("Successfully deleted existing submariner Endpoint %q", existing.Name)
	}

	return nil
}

func (d *DatastoreSyncer) startGatewayWatcher(stopCh <-chan struct{}) error {
	resourceWatcher, err := watcher.New(&watcher.Config{
		Scheme:     scheme.Scheme,
		RestConfig: d.syncerConfig.LocalRestConfig,
		RestMapper: d.syncerConfig.RestMapper,
		Client:     d.syncerConfig.LocalClient,
		ResourceConfigs: []watcher.ResourceConfig{
			{
				Name:                "Gateway watcher for datastoresyncer",
				ResourceType:        &submarinerv1.Gateway{},
				SourceNamespace:     d.syncerConfig.LocalNamespace,
				ResourcesEquivalent: d.areGatewaysEquivalent,
				SourceFieldSelector: fields.Set(map[string]string{"metadata.name": d.localEndpoint.Spec().Hostname}).AsSelector().String(),
				Handler: watcher.EventHandlerFuncs{
					OnCreateFunc: d.handleCreateOrUpdateGateway,
					OnUpdateFunc: d.handleCreateOrUpdateGateway,
					OnDeleteFunc: nil,
				},
			},
		},
	})
	if err != nil {
		return errors.Wrap(err, "error creating Gateway resource watcher")
	}

	err = resourceWatcher.Start(stopCh)
	if err != nil {
		return errors.Wrap(err, "error starting the Gateway resource watcher")
	}

	return nil
}

func (d *DatastoreSyncer) createLocalCluster(ctx context.Context, federator federate.Federator) error {
	logger.Infof("Creating local submariner Cluster: %s", resource.ToJSON(d.localCluster))

	cluster := &submarinerv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: resource.EnsureValidName(d.localCluster.Spec.ClusterID),
		},
		Spec: d.localCluster.Spec,
	}

	return federator.Distribute(ctx, cluster) //nolint:wrapcheck  // Let the caller wrap it
}

func (d *DatastoreSyncer) createOrUpdateLocalEndpoint(ctx context.Context, federator federate.Federator) error {
	localEndpoint := d.localEndpoint.Resource()

	logger.Infof("Creating local submariner Endpoint: %s", resource.ToJSON(localEndpoint))

	return federator.Distribute(ctx, localEndpoint) //nolint:wrapcheck  // Let the caller wrap it
}
