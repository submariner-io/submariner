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

package controller

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"github.com/kelseyhightower/envconfig"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type specification struct {
	ClusterID string
	Namespace string
}

type handlerStateImpl struct {
	isOnGateway     atomic.Bool
	wasOnGateway    bool
	remoteEndpoints sync.Map
}

func (s *handlerStateImpl) setIsOnGateway(v bool) {
	s.isOnGateway.Store(v)
}

func (s *handlerStateImpl) IsOnGateway() bool {
	return s.isOnGateway.Load()
}

func (s *handlerStateImpl) GetRemoteEndpoints() []subv1.Endpoint {
	var endpoints []subv1.Endpoint

	s.remoteEndpoints.Range(func(_, value any) bool {
		endpoints = append(endpoints, *value.(*subv1.Endpoint))
		return true
	})

	return endpoints
}

type Controller struct {
	endpointInformer cache.SharedInformer
	nodeInformer     cache.SharedInformer
	syncers          []syncer.Interface
	handlers         *event.Registry
}

type handlerController struct {
	syncMutex               sync.Mutex
	handlerState            handlerStateImpl
	handler                 event.Handler
	nodeHandler             event.NodeHandler
	hostname                string
	clusterID               string
	remoteEndpointTimeStamp map[string]v1.Time
}

// If the handler cannot recover from a failure, even after retrying for maximum requeue attempts,
// it's best to disregard the event. This prevents the logs from being flooded with repetitive errors.
const maxRequeues = 20

type Config struct {
	// Registry is the event handler registry where controller events will be sent.
	Registry   *event.Registry
	RestMapper meta.RESTMapper
	Client     dynamic.Interface
	Scheme     *runtime.Scheme
}

var logger = log.Logger{Logger: logf.Log.WithName("EventController")}

func New(config *Config) (*Controller, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, errors.Wrapf(err, "unable to read hostname")
	}

	ctl := Controller{
		handlers: config.Registry,
	}

	var env specification

	err = envconfig.Process("submariner", &env)
	if err != nil {
		return nil, errors.Wrap(err, "error processing env vars")
	}

	err = subv1.AddToScheme(scheme.Scheme)
	if err != nil {
		return nil, errors.Wrap(err, "error adding submariner types to the scheme")
	}

	syncerConfig := &syncer.ResourceSyncerConfig{
		SourceNamespace: env.Namespace,
		SourceClient:    config.Client,
		RestMapper:      config.RestMapper,
		Federator:       federate.NewNoopFederator(),
		ResourceType:    &subv1.Endpoint{},
	}

	ctl.endpointInformer, err = syncer.NewSharedInformer(syncerConfig)
	if err != nil {
		return nil, errors.Wrap(err, "error creating endpoint informer")
	}

	for _, handler := range config.Registry.GetHandlers() {
		err = ctl.setupHandlerController(handler, env.ClusterID, hostname, *syncerConfig)
		if err != nil {
			return nil, err
		}
	}

	return &ctl, nil
}

// Start starts the controller.
func (c *Controller) Start(stopCh <-chan struct{}) error {
	logger.Info("Starting the Event controller...")

	runInformer(c.endpointInformer, stopCh)
	runInformer(c.nodeInformer, stopCh)

	for _, syncer := range c.syncers {
		err := syncer.Start(stopCh)
		if err != nil {
			return errors.Wrap(err, "error starting resource syncer")
		}
	}

	logger.Info("Event controller started")

	return nil
}

func (c *Controller) Stop() {
	logger.Info("Event controller stopping")

	if err := c.handlers.StopHandlers(); err != nil {
		logger.Warningf("In Event Controller, StopHandlers returned error: %v", err)
	}
}

//nolint:gocritic // Ignore hugeParam - intentional copy.
func (c *Controller) setupHandlerController(handler event.Handler, clusterID, hostname string,
	syncerConfig syncer.ResourceSyncerConfig,
) error {
	hCtrl := &handlerController{
		handler:                 handler,
		hostname:                hostname,
		clusterID:               clusterID,
		remoteEndpointTimeStamp: map[string]v1.Time{},
	}

	handler.SetState(&hCtrl.handlerState)

	syncerConfig.Name = fmt.Sprintf("Endpoint watcher for handler %q", handler.GetName())

	syncerConfig.Transform = func(obj runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
		switch op {
		case syncer.Create:
			return nil, hCtrl.handleCreatedEndpoint(obj.(*subv1.Endpoint), numRequeues)
		case syncer.Update:
			return nil, hCtrl.handleUpdatedEndpoint(obj.(*subv1.Endpoint), numRequeues)
		case syncer.Delete:
			return nil, hCtrl.handleRemovedEndpoint(obj.(*subv1.Endpoint), numRequeues)
		}

		return nil, false
	}

	endpointSyncer, err := syncer.NewResourceSyncerWithSharedInformer(&syncerConfig, c.endpointInformer)
	if err != nil {
		return errors.Wrap(err, "error creating resource syncer")
	}

	c.syncers = append(c.syncers, endpointSyncer)

	var ok bool

	hCtrl.nodeHandler, ok = hCtrl.handler.(event.NodeHandler)
	if ok {
		syncerConfig.Name = fmt.Sprintf("Node watcher for handler %q", handler.GetName())
		syncerConfig.ResourceType = &k8sv1.Node{}
		syncerConfig.SourceNamespace = k8sv1.NamespaceAll

		if c.nodeInformer == nil {
			c.nodeInformer, err = syncer.NewSharedInformer(&syncerConfig)
			if err != nil {
				return errors.Wrap(err, "error creating node informer")
			}
		}

		syncerConfig.Transform = func(obj runtime.Object, numRequeues int, op syncer.Operation) (runtime.Object, bool) {
			switch op {
			case syncer.Create:
				return nil, hCtrl.handleCreatedNode(obj.(*k8sv1.Node), numRequeues)
			case syncer.Update:
				return nil, hCtrl.handleUpdatedNode(obj.(*k8sv1.Node), numRequeues)
			case syncer.Delete:
				return nil, hCtrl.handleRemovedNode(obj.(*k8sv1.Node), numRequeues)
			}

			return nil, false
		}

		nodeSyncer, err := syncer.NewResourceSyncerWithSharedInformer(&syncerConfig, c.nodeInformer)
		if err != nil {
			return errors.Wrap(err, "error creating resource syncer")
		}

		c.syncers = append(c.syncers, nodeSyncer)
	}

	return nil
}

func runInformer(informer cache.SharedInformer, stopCh <-chan struct{}) {
	if informer != nil {
		go func() {
			informer.Run(stopCh)
		}()
	}
}
