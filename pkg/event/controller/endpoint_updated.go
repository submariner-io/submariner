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
	"k8s.io/klog/v2"
)

func (c *Controller) handleUpdatedEndpoint(obj runtime.Object, numRequeues int) bool {
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
	return c.handlers.LocalEndpointUpdated(endpoint) //nolint:wrapcheck  // Let the caller wrap it
}

func (c *Controller) handleUpdatedRemoteEndpoint(endpoint *smv1.Endpoint) error {
	return c.handlers.RemoteEndpointUpdated(endpoint) //nolint:wrapcheck  // Let the caller wrap it
}
