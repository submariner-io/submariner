package controller

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"

	smv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

func (c *Controller) handleUpdatedEndpoint(obj runtime.Object) bool {
	endpoint := obj.(*smv1.Endpoint)
	var err error
	if endpoint.Spec.ClusterID != c.env.ClusterID {
		err = c.handleUpdatedRemoteEndpoint(endpoint)
	} else {
		err = c.handleUpdatedLocalEndpoint(endpoint)
	}

	if err != nil {
		klog.Errorf("Error handling updated endpoint %+v", err)
	}

	return err != nil
}

func (c *Controller) handleUpdatedLocalEndpoint(endpoint *smv1.Endpoint) error {
	return c.handlers.LocalEndpointUpdated(endpoint)
}

func (c *Controller) handleUpdatedRemoteEndpoint(endpoint *smv1.Endpoint) error {
	return c.handlers.RemoteEndpointUpdated(endpoint)
}
