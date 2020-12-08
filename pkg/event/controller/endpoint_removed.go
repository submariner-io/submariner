package controller

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"

	smv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

func (c *Controller) handleRemovedEndpoint(obj runtime.Object) bool {
	endpoint := obj.(*smv1.Endpoint)

	var err error
	if endpoint.Spec.ClusterID != c.env.ClusterID {
		err = c.handleRemovedRemoteEndpoint(endpoint)
	} else {
		err = c.handleRemovedLocalEndpoint(endpoint)
	}

	if err != nil {
		klog.Errorf("Error handling removed endpoint %+v", err)
	}

	return err != nil
}

func (c *Controller) handleRemovedLocalEndpoint(endpoint *smv1.Endpoint) error {
	if err := c.handlers.LocalEndpointRemoved(endpoint); err != nil {
		return err
	}

	c.syncMutex.Lock()
	defer c.syncMutex.Unlock()

	if c.isGatewayNode && endpoint.Spec.Hostname == c.hostname {
		if err := c.handlers.TransitionToNonGateway(); err != nil {
			return err
		}

		c.isGatewayNode = false
	}

	return nil
}

func (c *Controller) handleRemovedRemoteEndpoint(endpoint *smv1.Endpoint) error {
	return c.handlers.RemoteEndpointRemoved(endpoint)
}
