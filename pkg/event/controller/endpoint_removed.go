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
	smv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func (c *Controller) handleRemovedEndpoint(obj runtime.Object, requeueCount int) bool {
	endpoint := obj.(*smv1.Endpoint)

	if requeueCount > maxRequeues {
		logger.Errorf(nil, "Ignoring delete event for endpoint %q, as its requeued for more than %d times",
			endpoint.Spec.ClusterID, maxRequeues)
		return false
	}

	c.syncMutex.Lock()
	defer c.syncMutex.Unlock()

	var err error
	if endpoint.Spec.ClusterID != c.env.ClusterID {
		err = c.handleRemovedRemoteEndpoint(endpoint)
	} else {
		err = c.handleRemovedLocalEndpoint(endpoint)
	}

	if err != nil {
		logger.Error(err, "Error handling removed endpoint")
	}

	return err != nil
}

func (c *Controller) handleRemovedLocalEndpoint(endpoint *smv1.Endpoint) error {
	if endpoint.Spec.Hostname == c.hostname {
		c.handlerState.setIsOnGateway(false)
	}

	err := c.handlers.LocalEndpointRemoved(endpoint)

	if err == nil && c.handlerState.wasOnGateway && !c.handlerState.IsOnGateway() {
		logger.Infof("Transitioned to non-gateway node %q", endpoint.Spec.Hostname)

		err = c.handlers.TransitionToNonGateway()
	}

	if err == nil {
		c.handlerState.wasOnGateway = c.handlerState.IsOnGateway()
	}

	return err //nolint:wrapcheck  // Let the caller wrap it
}

func (c *Controller) handleRemovedRemoteEndpoint(endpoint *smv1.Endpoint) error {
	c.handlerState.remoteEndpoints.Delete(endpoint.Name)
	return c.handlers.RemoteEndpointRemoved(endpoint) //nolint:wrapcheck  // Let the caller wrap it
}
