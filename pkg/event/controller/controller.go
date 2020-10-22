package controller

import (
	"fmt"
	"os"
	"sync"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/watcher"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"

	"github.com/kelseyhightower/envconfig"

	subv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/event"
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

func New(registry *event.Registry, masterURL, kubeconfig string) (*Controller, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("Unable to read hostname: %v", err)
	}

	ctl := Controller{
		handlers:  registry,
		syncMutex: &sync.Mutex{},
		hostname:  hostname,
	}

	err = envconfig.Process("submariner", &ctl.env)
	if err != nil {
		return nil, err
	}

	err = subv1.AddToScheme(scheme.Scheme)
	if err != nil {
		return nil, fmt.Errorf("Error adding submariner types to the scheme: %v", err)
	}

	cfg, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("Error building config from flags %v", err.Error())
	}

	ctl.resourceWatcher, err = watcher.New(&watcher.Config{
		Scheme:     scheme.Scheme,
		RestConfig: cfg,
		ResourceConfigs: []watcher.ResourceConfig{
			{
				Name:            fmt.Sprintf("Endpoint watcher for %s registry", registry.GetName()),
				ResourceType:    &subv1.Endpoint{},
				SourceNamespace: ctl.env.Namespace,
				Handler: watcher.EventHandlerFuncs{
					OnCreateFunc: ctl.handleCreatedEndpoint,
					OnUpdateFunc: ctl.handleUpdatedEndpoint,
					OnDeleteFunc: ctl.handleRemovedEndpoint,
				},
			}, {
				Name:                fmt.Sprintf("Node watcher for %s registry", registry.GetName()),
				ResourceType:        &k8sv1.Node{},
				ResourcesEquivalent: ctl.isNodeEquivalent,
				Handler: watcher.EventHandlerFuncs{
					OnCreateFunc: ctl.handleCreatedNode,
					OnUpdateFunc: ctl.handleUpdatedNode,
					OnDeleteFunc: ctl.handleRemovedNode,
				},
			},
		},
	})

	if err != nil {
		return nil, errors.Wrapf(err, "Error creating resource watcher")
	}

	return &ctl, nil
}

// Start will start the controller, and wait until stopCh receives a message
func (c *Controller) Start(stopCh <-chan struct{}) error {
	klog.Info("Starting Controller")

	err := c.resourceWatcher.Start(stopCh)
	if err != nil {
		return err
	}

	klog.Info("Event Controller workers started")
	<-stopCh
	klog.Info("Event Controller stopping")

	// TODO: Detect if it's an uninstall and invoke StopHandlers with uninstall=true if it's the case
	if err := c.handlers.StopHandlers(false); err != nil {
		klog.Warningf("In Event Controller, StopHandlers returned error: %v", err)
	}

	return nil
}
