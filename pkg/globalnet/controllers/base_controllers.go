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
package controllers

import (
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/submariner/pkg/ipam"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func newBaseController() *baseController {
	return &baseController{
		stopCh: make(chan struct{}),
	}
}

func (c *baseController) Stop() {
	close(c.stopCh)
}

func newBaseIPAllocationController(pool *ipam.IPPool) *baseIPAllocationController {
	return &baseIPAllocationController{
		baseController: newBaseController(),
		pool:           pool,
	}
}

func (c *baseIPAllocationController) Start() error {
	return c.resourceSyncer.Start(c.stopCh)
}

// nolint unparam - 'federator' will be used
func (c *baseIPAllocationController) reserveAllocatedIPs(federator federate.Federator, obj *unstructured.Unstructured) error {
	var err error

	ips, ok, _ := unstructured.NestedStringSlice(obj.Object, "status", "allocatedIPs")
	if ok {
		err = c.pool.Reserve(ips...)
		if err != nil {
			// TODO: null out the allocatedIPs
			return err
		}
	}

	// if err != nil {
	//	 TODO: call federator.Distribute to update the obj
	// }

	return nil
}
