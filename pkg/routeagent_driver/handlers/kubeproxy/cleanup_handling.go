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
package kubeproxy

import (
	"github.com/submariner-io/admiral/pkg/log"

	"github.com/submariner-io/submariner/pkg/routeagent_driver/cleanup"

	"k8s.io/klog"
)

func (kp *SyncHandler) InstallCleanupHandlers(handlers ...cleanup.Handler) {
	kp.cleanupHandlers = append(kp.cleanupHandlers, handlers...)
}

func (kp *SyncHandler) nonGatewayCleanups() {
	klog.V(log.DEBUG).Infof("Handling nonGatewayCleanups")

	for _, handler := range kp.cleanupHandlers {
		if err := handler.NonGatewayCleanup(); err != nil {
			klog.Errorf("Error handling NonGatewayCleanup in %q, %s",
				handler.GetName(), err)
		}
	}
}

func (kp *SyncHandler) gatewayToNonGatewayTransitionCleanups() {
	klog.V(log.DEBUG).Infof("Handling gatewayToNonGatewayTransitionCleanups: wasGatewayPreviously %t ",
		kp.wasGatewayPreviously)

	if kp.wasGatewayPreviously {
		kp.wasGatewayPreviously = false
		for _, handler := range kp.cleanupHandlers {
			if err := handler.GatewayToNonGatewayTransition(); err != nil {
				klog.Errorf("Error handling GatewayToNonGateway cleanup in %q, %s", handler.GetName(), err)
			}
		}
	}
}
