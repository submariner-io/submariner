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

func (c *handlerController) handleCreatedEndpoint(endpoint *smv1.Endpoint, requeueCount int) bool {
	var err error

	if requeueCount > maxRequeues {
		logger.Errorf(nil, "Ignoring create event for endpoint %q, as its requeued for more than %d times",
			endpoint.Spec.ClusterID, maxRequeues)
		return false
	}

	c.syncMutex.Lock()
	defer c.syncMutex.Unlock()

	if endpoint.Spec.ClusterID != c.clusterID {
		err = c.handleCreatedRemoteEndpoint(endpoint)
	} else {
		err = c.handleCreatedLocalEndpoint(endpoint)
	}

	if err != nil {
		logger.Error(err, "Error handling created endpoint")
	}

	return err != nil
}

func (c *handlerController) handleCreatedLocalEndpoint(endpoint *smv1.Endpoint) error {
	if endpoint.Spec.Hostname == c.hostname {
		c.handlerState.setIsOnGateway(true)
	}

	err := c.handler.LocalEndpointCreated(endpoint)

	if err == nil && !c.handlerState.wasOnGateway && c.handlerState.IsOnGateway() {
		logger.Infof("Transitioned to gateway node %q with endpoint private IP %s", c.hostname, endpoint.Spec.PrivateIP)

		err = c.handler.TransitionToGateway()
	}

	if err == nil {
		c.handlerState.wasOnGateway = c.handlerState.IsOnGateway()
	}

	return err //nolint:wrapcheck  // Let the caller wrap it
}

func (c *handlerController) handleCreatedRemoteEndpoint(endpoint *smv1.Endpoint) error {
	lastProcessedTime, ok := c.remoteEndpointTimeStamp[endpoint.Spec.ClusterID]

	if ok && lastProcessedTime.After(endpoint.CreationTimestamp.Time) {
		logger.Infof("Ignoring new remote %#v since a later endpoint was already processed", endpoint)
		return nil
	}

	c.handlerState.remoteEndpoints.Store(endpoint.Name, endpoint)
	c.remoteEndpointTimeStamp[endpoint.Spec.ClusterID] = endpoint.CreationTimestamp

	return c.handler.RemoteEndpointCreated(endpoint) //nolint:wrapcheck  // Let the caller wrap it
}
