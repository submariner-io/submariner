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
	"github.com/submariner-io/admiral/pkg/global"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/resource"
	resourceSyncer "github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/watcher"
	"github.com/submariner-io/admiral/pkg/workqueue"
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
	syncer        *broker.Syncer
}

const maxRemoteEndpointRequeues = 20

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

	syncer, err := d.createSyncer(ctx)
	if err != nil {
		return err
	}

	d.syncer = syncer

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

	if err := d.createOrUpdateLocalEndpoint(ctx); err != nil {
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
	syncer, err := d.createSyncer(ctx)
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

func (d *DatastoreSyncer) createSyncer(ctx context.Context) (*broker.Syncer, error) {
	d.syncerConfig.ResourceConfigs = []broker.ResourceConfig{
		{
			LocalSourceNamespace:  d.syncerConfig.LocalNamespace,
			LocalResourceType:     &submarinerv1.Cluster{},
			LocalWorkQueueConfig:  workqueue.ConfigFromGlobal("local-submariner-cluster", nil),
			BrokerResourceType:    &submarinerv1.Cluster{},
			BrokerWorkQueueConfig: workqueue.ConfigFromGlobal("broker-submariner-cluster", nil),
		},
		{
			LocalSourceNamespace:   d.syncerConfig.LocalNamespace,
			LocalResourceType:      &submarinerv1.Endpoint{},
			LocalWorkQueueConfig:   workqueue.ConfigFromGlobal("local-submariner-endpoint", nil),
			TransformBrokerToLocal: d.shouldSyncRemoteEndpoint,
			BrokerResourceType:     &submarinerv1.Endpoint{},
			BrokerWorkQueueConfig:  workqueue.ConfigFromGlobal("broker-submariner-endpointr", nil),
		},
	}

	d.syncerConfig.MaxLogVerbosity = global.Get("datastore-broker.syncer.max-verbosity", 0)

	syncer, err := broker.NewSyncer(ctx, d.syncerConfig)

	return syncer, errors.Wrap(err, "error creating the syncer")
}

func (d *DatastoreSyncer) shouldSyncRemoteEndpoint(obj runtime.Object, numRequeues int,
	op resourceSyncer.Operation,
) (runtime.Object, bool) {
	remoteEndpoint := obj.(*submarinerv1.Endpoint)

	if op == resourceSyncer.Delete {
		return obj, false
	}

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

	if rejected, requeue := d.validateRemoteEndpointSubnets(remoteEndpoint, numRequeues); rejected {
		return nil, requeue
	}

	return obj, false
}

// validateRemoteEndpointSubnets ensures that subnets in a broker-supplied remote Endpoint are
// consistent with that cluster's declared CIDRs and do not conflict with subnets already
// accepted from other clusters. A remote Endpoint is rejected unless every Spec.Subnets entry
// (i) is wholly contained in the CIDRs declared by that cluster's own Cluster CR
// (Service/Cluster/Global), and (ii) does not overlap any subnet already accepted from a
// different remote cluster. Returns (rejected, requeue).
//
//nolint:gocyclo // Method is not really that complex.
func (d *DatastoreSyncer) validateRemoteEndpointSubnets(remoteEndpoint *submarinerv1.Endpoint, numRequeues int) (bool, bool) {
	if len(remoteEndpoint.Spec.Subnets) == 0 || d.syncer == nil {
		return false, false
	}

	var remoteCluster *submarinerv1.Cluster

	for _, o := range d.syncer.ListLocalResources(&submarinerv1.Cluster{}) {
		c := o.(*submarinerv1.Cluster)
		if c.Spec.ClusterID == remoteEndpoint.Spec.ClusterID {
			remoteCluster = c
			break
		}
	}

	if remoteCluster == nil {
		if numRequeues < maxRemoteEndpointRequeues {
			logger.V(log.DEBUG).Infof("Cluster CR for %q not yet synced; re-queueing remote Endpoint %q",
				remoteEndpoint.Spec.ClusterID, remoteEndpoint.Name)

			return true, true
		}

		logger.Errorf(nil, "Rejecting remote Endpoint %q: no Cluster CR found for cluster %q after %d retries",
			remoteEndpoint.Name, remoteEndpoint.Spec.ClusterID, numRequeues)

		return true, false
	}

	allowed := make([]string, 0, len(remoteCluster.Spec.ServiceCIDR)+len(remoteCluster.Spec.ClusterCIDR)+len(remoteCluster.Spec.GlobalCIDR))
	allowed = append(allowed, remoteCluster.Spec.ServiceCIDR...)
	allowed = append(allowed, remoteCluster.Spec.ClusterCIDR...)
	allowed = append(allowed, remoteCluster.Spec.GlobalCIDR...)

	for _, subnet := range remoteEndpoint.Spec.Subnets {
		contained, err := cidr.IsContained(allowed, subnet)
		if err != nil {
			logger.Errorf(err, "Rejecting remote Endpoint %q: invalid subnet %q", remoteEndpoint.Name, subnet)
			return true, false
		}

		if !contained {
			logger.Errorf(nil, "Rejecting remote Endpoint %q: subnet %q is not within Cluster %q declared CIDRs %v",
				remoteEndpoint.Name, subnet, remoteCluster.Spec.ClusterID, allowed)

			return true, false
		}
	}

	for _, o := range d.syncer.ListLocalResources(&submarinerv1.Endpoint{}) {
		other := o.(*submarinerv1.Endpoint)
		if other.Spec.ClusterID == remoteEndpoint.Spec.ClusterID || other.Spec.ClusterID == d.localCluster.Spec.ClusterID {
			continue
		}

		for _, subnet := range remoteEndpoint.Spec.Subnets {
			overlap, err := cidr.IsOverlapping(other.Spec.Subnets, subnet)
			if err != nil {
				logger.Errorf(err, "Rejecting remote Endpoint %q: unable to validate overlap with cluster %q",
					remoteEndpoint.Name, other.Spec.ClusterID)

				return true, false
			}

			if !overlap {
				continue
			}

			logger.Errorf(nil, "Rejecting remote Endpoint %q: subnet %q overlaps subnet already accepted from cluster %q (%v)",
				remoteEndpoint.Name, subnet, other.Spec.ClusterID, other.Spec.Subnets)

			return true, false
		}
	}

	return false, false
}

func (d *DatastoreSyncer) ensureExclusiveEndpoint(ctx context.Context, syncer *broker.Syncer) error {
	logger.Info("Ensuring we are the only endpoint active for this cluster")

	endpoints := syncer.ListLocalResources(&submarinerv1.Endpoint{})
	for i := range endpoints {
		existing := endpoints[i].(*submarinerv1.Endpoint)
		if existing.Spec.ClusterID != d.localCluster.Spec.ClusterID {
			continue
		}

		if existing.Spec.Equals(d.localEndpoint.Spec()) {
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
				WorkQueueConfig: workqueue.ConfigFromGlobal("gateway", nil),
				MaxLogVerbosity: global.Get("gateway.watcher.max-verbosity", 0),
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

func (d *DatastoreSyncer) createOrUpdateLocalEndpoint(ctx context.Context) error {
	logger.Infof("Creating local submariner Endpoint: %s", resource.ToJSON(d.localEndpoint.Resource()))

	return d.localEndpoint.Create(ctx) //nolint:wrapcheck  // Let the caller wrap it
}
