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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"

	smv1 "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
)

func (c *Controller) handleCreatedEndpoint(obj runtime.Object, numRequeues int) bool {
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
