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

	"github.com/kelseyhightower/envconfig"
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/watcher"
	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type specification struct {
	ClusterID string
	Namespace string
}

type Controller struct {
	env             specification
	resourceWatcher watcher.Interface

	handlers *event.Registry

	syncMutex     *sync.Mutex
	hostname      string
	isGatewayNode bool
}

type Config struct {
	// Registry is the event handler registry where controller events will be sent.
	Registry *event.Registry

	// MasterURL accepts the K8s API URL. By default the in-cluster-config will be used.
	MasterURL string

	// Kubeconfig accepts the kubeconfig with the cluster credentials. By default the in-cluster-config will be used.
	Kubeconfig string

	// RestMapper can be provided for unit testing. By default New will create its own RestMapper.
	RestMapper meta.RESTMapper

	// Client can be provided for unit testing. By default New will create its own dynamic client.
	Client dynamic.Interface
}

var logger = log.Logger{Logger: logf.Log.WithName("EventController")}

func New(config *Config) (*Controller, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, errors.Wrapf(err, "unable to read hostname")
	}

	ctl := Controller{
		handlers:  config.Registry,
		syncMutex: &sync.Mutex{},
		hostname:  hostname,
	}

	err = envconfig.Process("submariner", &ctl.env)
	if err != nil {
		return nil, errors.Wrap(err, "error processing env vars")
	}

	err = subv1.AddToScheme(scheme.Scheme)
	if err != nil {
		return nil, errors.Wrap(err, "error adding submariner types to the scheme")
	}

	var cfg *restclient.Config
	if config.Client == nil {
		cfg, err = clientcmd.BuildConfigFromFlags(config.MasterURL, config.Kubeconfig)
		if err != nil {
			return nil, errors.Wrap(err, "error building config from flags")
		}
	}

	ctl.resourceWatcher, err = watcher.New(&watcher.Config{
		Scheme:     scheme.Scheme,
		RestConfig: cfg,
		ResourceConfigs: []watcher.ResourceConfig{
			{
				Name:            fmt.Sprintf("Endpoint watcher for %s registry", ctl.handlers.GetName()),
				ResourceType:    &subv1.Endpoint{},
				SourceNamespace: ctl.env.Namespace,
				Handler: watcher.EventHandlerFuncs{
					OnCreateFunc: ctl.handleCreatedEndpoint,
					OnUpdateFunc: ctl.handleUpdatedEndpoint,
					OnDeleteFunc: ctl.handleRemovedEndpoint,
				},
			}, {
				Name:                fmt.Sprintf("Node watcher for %s registry", ctl.handlers.GetName()),
				ResourceType:        &k8sv1.Node{},
				ResourcesEquivalent: ctl.isNodeEquivalent,
				Handler: watcher.EventHandlerFuncs{
					OnCreateFunc: ctl.handleCreatedNode,
					OnUpdateFunc: ctl.handleUpdatedNode,
					OnDeleteFunc: ctl.handleRemovedNode,
				},
			},
		},
		Client:     config.Client,
		RestMapper: config.RestMapper,
	})

	if err != nil {
		return nil, errors.Wrap(err, "error creating resource watcher")
	}

	return &ctl, nil
}

// Start starts the controller.
func (c *Controller) Start(stopCh <-chan struct{}) error {
	logger.Info("Starting the Event controller...")

	err := c.resourceWatcher.Start(stopCh)
	if err != nil {
		return errors.Wrap(err, "error starting the resource watcher")
	}

	logger.Info("Event controller started")

	return nil
}

func (c *Controller) Stop() {
	logger.Info("Event controller stopping")

	if err := c.handlers.StopHandlers(false); err != nil {
		logger.Warningf("In Event Controller, StopHandlers returned error: %v", err)
	}
}
