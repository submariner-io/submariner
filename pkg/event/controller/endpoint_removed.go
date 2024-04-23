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
)

func (c *handlerController) handleRemovedEndpoint(endpoint *smv1.Endpoint, requeueCount int) bool {
	if requeueCount > maxRequeues {
		logger.Errorf(nil, "Ignoring delete event for endpoint %q, as its requeued for more than %d times",
			endpoint.Spec.ClusterID, maxRequeues)
		return false
	}

	c.syncMutex.Lock()
	defer c.syncMutex.Unlock()

	var err error
	if endpoint.Spec.ClusterID != c.clusterID {
		err = c.handleRemovedRemoteEndpoint(endpoint)
	} else {
		err = c.handleRemovedLocalEndpoint(endpoint)
	}

	if err != nil {
		logger.Error(err, "Error handling removed endpoint")
	}

	return err != nil
}

func (c *handlerController) handleRemovedLocalEndpoint(endpoint *smv1.Endpoint) error {
	if endpoint.Spec.Hostname == c.hostname {
		c.handlerState.setIsOnGateway(false)
	}

	err := c.handler.LocalEndpointRemoved(endpoint)

	if err == nil && c.handlerState.wasOnGateway && !c.handlerState.IsOnGateway() {
		logger.Infof("Transitioned to non-gateway node %q", endpoint.Spec.Hostname)

		err = c.handler.TransitionToNonGateway()
	}

	if err == nil {
		c.handlerState.wasOnGateway = c.handlerState.IsOnGateway()
	}

	return err //nolint:wrapcheck  // Let the caller wrap it
}

func (c *handlerController) handleRemovedRemoteEndpoint(endpoint *smv1.Endpoint) error {
	lastProcessedTime, ok := c.remoteEndpointTimeStamp[endpoint.Spec.ClusterID]

	if ok && lastProcessedTime.After(endpoint.CreationTimestamp.Time) {
		logger.Infof("Ignoring deleted remote %#v since a later endpoint was already"+
			"processed", endpoint)
		return nil
	}

	delete(c.remoteEndpointTimeStamp, endpoint.Spec.ClusterID)
	c.handlerState.remoteEndpoints.Delete(endpoint.Name)

	return c.handler.RemoteEndpointRemoved(endpoint) //nolint:wrapcheck  // Let the caller wrap it
}
