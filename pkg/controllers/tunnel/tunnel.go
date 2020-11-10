package tunnel

import (
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/watcher"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cableengine"
	"github.com/submariner-io/submariner/pkg/types"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
)

type controller struct {
	engine cableengine.Engine
}

func StartController(engine cableengine.Engine, namespace string, config *watcher.Config, stopCh <-chan struct{}) error {
	klog.Info("Starting the tunnel controller")

	c := &controller{engine: engine}

	config.ResourceConfigs = []watcher.ResourceConfig{
		{
			Name:         "Tunnel Controller",
			ResourceType: &v1.Endpoint{},
			Handler: watcher.EventHandlerFuncs{
				OnCreateFunc: c.handleCreatedOrUpdatedEndpoint,
				OnUpdateFunc: c.handleCreatedOrUpdatedEndpoint,
				OnDeleteFunc: c.handleRemovedEndpoint,
			},
			SourceNamespace: namespace,
		},
	}

	endpointWatcher, err := watcher.New(config)
	if err != nil {
		return err
	}

	err = endpointWatcher.Start(stopCh)
	if err != nil {
		return err
	}

	return nil
}

func (c *controller) handleCreatedOrUpdatedEndpoint(obj runtime.Object) bool {
	endpoint := obj.(*v1.Endpoint)

	klog.V(log.DEBUG).Infof("Tunnel controller processing added or updated submariner Endpoint object: %#v", endpoint)

	myEndpoint := types.SubmarinerEndpoint{
		Spec: endpoint.Spec,
	}

	err := c.engine.InstallCable(myEndpoint)
	if err != nil {
		klog.Errorf("error installing cable for Endpoint %#v, %v", myEndpoint, err)
		return true
	}

	klog.V(log.DEBUG).Infof("Tunnel controller successfully installed Endpoint cable %s in the engine", endpoint.Spec.CableName)

	return false
}

func (c *controller) handleRemovedEndpoint(obj runtime.Object) bool {
	endpoint := obj.(*v1.Endpoint)

	klog.V(log.DEBUG).Infof("Tunnel controller processing removed submariner Endpoint object: %#v", endpoint)

	myEndpoint := types.SubmarinerEndpoint{
		Spec: endpoint.Spec,
	}

	if err := c.engine.RemoveCable(myEndpoint); err != nil {
		klog.Errorf("Tunnel controller failed to remove Endpoint cable %#v from the engine: %v", myEndpoint, err)
		return true
	}

	klog.V(log.DEBUG).Infof("Tunnel controller successfully removed Endpoint cable %s from the engine", endpoint.Spec.CableName)

	return false
}
