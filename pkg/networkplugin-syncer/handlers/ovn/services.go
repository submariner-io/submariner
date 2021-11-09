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

package ovn

import (
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"k8s.io/klog"
)

func (ovn *SyncHandler) ensureServiceLoadBalancersFrom(logicalSwitch string) error {
	lbIds, err := ovn.getK8sLoadBalancerIDsFor(logicalSwitch)
	if err != nil {
		return err
	}

	submarinerRouterID, err := ovn.getSubmarinerRouterID()
	if err != nil {
		return err
	}

	return ovn.nbctl.LrSetLoadBalancers(submarinerRouterID, lbIds) // nolint:wrapcheck  // No need to wrap this error
}

func (ovn *SyncHandler) getSubmarinerRouterID() (string, error) {
	submarinerRouter, err := ovn.nbdb.LRGet(submarinerLogicalRouter)
	if err != nil {
		return "", errors.Wrapf(err, "error getting %q router ID", submarinerLogicalRouter)
	}

	if len(submarinerRouter) != 1 {
		return "", fmt.Errorf("only one %q router expected, but found %#v", submarinerLogicalRouter, submarinerRouter)
	}

	return submarinerRouter[0].UUID, nil
}

func (ovn *SyncHandler) getK8sLoadBalancerIDsFor(logicalSwitch string) ([]string, error) {
	loadBalancers, err := ovn.nbdb.LSLBList(logicalSwitch)
	if err != nil {
		return nil, errors.Wrapf(err, "error listing load balancers for logical switch %q", logicalSwitch)
	}

	var lbIds []string

	for _, lb := range loadBalancers {
		for key := range lb.ExternalID {
			externalID, ok := key.(string)
			if !ok {
				klog.Warningf("Unable to extract key from load-balancer %q external ids %#v", lb.UUID, key)
				continue
			}

			if strings.Contains(externalID, "k8s-cluster-lb") {
				lbIds = append(lbIds, lb.UUID)
			}
		}
	}

	return lbIds, nil
}
