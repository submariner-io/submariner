package tunnel

import (
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/admiral/pkg/syncer"
	v1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"github.com/submariner-io/submariner/pkg/cableengine"
	"github.com/submariner-io/submariner/pkg/types"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog"
)

type controller struct {
	engine cableengine.Engine
}

func StartController(engine cableengine.Engine, namespace string, client dynamic.Interface, restMapper meta.RESTMapper,
	scheme *runtime.Scheme, stopCh <-chan struct{}) error {
	klog.Info("Starting the tunnel controller")

	c := &controller{engine: engine}

	endpointSyncer, err := syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "Tunnel controller",
		SourceClient:    client,
		SourceNamespace: namespace,
		Direction:       syncer.LocalToRemote,
		RestMapper:      restMapper,
		Federator:       federate.NewNoopFederator(),
		ResourceType:    &v1.Endpoint{},
		Transform:       c.processEndpoint,
		Scheme:          scheme,
	})
	if err != nil {
		return err
	}

	err = endpointSyncer.Start(stopCh)
	if err != nil {
		return err
	}

	return nil
}

func (c *controller) processEndpoint(obj runtime.Object, op syncer.Operation) (runtime.Object, bool) {
	endpoint := obj.(*v1.Endpoint)

	if op == syncer.Delete {
		return nil, c.handleRemovedEndpoint(endpoint)
	}

	klog.V(log.DEBUG).Infof("Tunnel controller processing added or updated submariner Endpoint object: %#v", endpoint)

	myEndpoint := types.SubmarinerEndpoint{
		Spec: endpoint.Spec,
	}

	err := c.engine.InstallCable(myEndpoint)
	if err != nil {
		klog.Errorf("error installing cable for Endpoint %#v, %v", myEndpoint, err)
		return nil, true
	}

	klog.V(log.DEBUG).Infof("Tunnel controller successfully installed Endpoint cable %s in the engine", endpoint.Spec.CableName)

	return nil, false
}

func (c *controller) handleRemovedEndpoint(endpoint *v1.Endpoint) bool {
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
