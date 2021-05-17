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
package cleanup

import (
	"fmt"
	"syscall"

	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/netlink"
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/routeagent_driver/cleanup"
)

type XFRMCleanupHandler struct {
	netLink netlink.Interface
}

func NewXFRMCleanupHandler() cleanup.Handler {
	return &XFRMCleanupHandler{netLink: netlink.New()}
}

func (xc *XFRMCleanupHandler) GetName() string {
	return "XFRM cleanup handler"
}

func (xc *XFRMCleanupHandler) NonGatewayCleanup() error {
	currentXfrmPolicyList, err := xc.netLink.XfrmPolicyList(syscall.AF_INET)

	if err != nil {
		return fmt.Errorf("error retrieving current xfrm policies: %v", err)
	}

	if len(currentXfrmPolicyList) > 0 {
		klog.Infof("Cleaning up %d XFRM policies", len(currentXfrmPolicyList))
	}

	for i := range currentXfrmPolicyList {
		klog.V(log.DEBUG).Infof("Deleting XFRM policy %s", currentXfrmPolicyList[i])

		if err = xc.netLink.XfrmPolicyDel(&currentXfrmPolicyList[i]); err != nil {
			return fmt.Errorf("error deleting XFRM policy %s: %v", currentXfrmPolicyList[i], err)
		}
	}

	return nil
}

func (xc *XFRMCleanupHandler) GatewayToNonGatewayTransition() error {
	return nil
}
