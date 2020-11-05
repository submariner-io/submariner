package controller

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"

	smv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

func (c *Controller) handleCreatedEndpoint(obj runtime.Object) bool {
	var err error

	endpoint := obj.(*smv1.Endpoint)

	if endpoint.Spec.ClusterID != c.env.ClusterID {
		err = c.handleCreatedRemoteEndpoint(endpoint)
	} else {
		err = c.handleCreatedLocalEndpoint(endpoint)
	}

	if err != nil {
		klog.Errorf("Error handling created endpoint: %v", err)
	}

	return err != nil
}

func (c *Controller) handleCreatedLocalEndpoint(endpoint *smv1.Endpoint) error {
	if err := c.handlers.LocalEndpointCreated(endpoint); err != nil {
		return err
	}

	if endpoint.Spec.Hostname == c.hostname {
		c.syncMutex.Lock()
		defer c.syncMutex.Unlock()

		// Verify if this node was a GatewayNode already. If not, it just transitioned to Gateway Node.
		if !c.isGatewayNode {
			if err := c.handlers.TransitionToGateway(); err != nil {
				return err
			}
		}

		c.isGatewayNode = true
	}

	return nil
}

func (c *Controller) handleCreatedRemoteEndpoint(endpoint *smv1.Endpoint) error {
	if err := c.handlers.RemoteEndpointCreated(endpoint); err != nil {
		return err
	}

	return nil
}
