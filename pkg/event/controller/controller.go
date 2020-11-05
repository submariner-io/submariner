package controller

import (
	"fmt"
	"os"
	"sync"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/watcher"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	restclient "k8s.io/client-go/rest"
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

func New(config *Config) (*Controller, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("Unable to read hostname: %v", err)
	}

	ctl := Controller{
		handlers:  config.Registry,
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

	var cfg *restclient.Config
	if config.Client == nil {
		cfg, err = clientcmd.BuildConfigFromFlags(config.MasterURL, config.MasterURL)
		if err != nil {
			return nil, fmt.Errorf("Error building config from flags %v", err.Error())
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
