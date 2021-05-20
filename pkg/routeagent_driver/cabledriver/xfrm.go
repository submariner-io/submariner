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
package cabledriver

import (
	"fmt"
	"syscall"

	"github.com/submariner-io/submariner/pkg/event"

	"github.com/submariner-io/admiral/pkg/log"
	"github.com/submariner-io/submariner/pkg/netlink"
	"k8s.io/klog"
)

type Handler struct {
	event.HandlerBase
	netLink netlink.Interface
}

func NewXfrm() *Handler {
	return &Handler{netLink: netlink.New()}
}

func (xfrm *Handler) GetHandlerType() event.HandlerType {
	return event.CableDriver
}

func (xfrm *Handler) GetName() string {
	return "xfrm"
}

func (xfrm *Handler) GetDrivers() []string {
	return []string{"libreswan", "wireguard", "default"}
}

func (xfrm *Handler) TransitionToNonGateway() error {
	currentXfrmPolicyList, err := xfrm.netLink.XfrmPolicyList(syscall.AF_INET)

	if err != nil {
		return fmt.Errorf("error retrieving current xfrm policies: %v", err)
	}

	if len(currentXfrmPolicyList) > 0 {
		klog.Infof("Cleaning up %d XFRM policies", len(currentXfrmPolicyList))
	}

	for i := range currentXfrmPolicyList {
		klog.V(log.DEBUG).Infof("Deleting XFRM policy %s", currentXfrmPolicyList[i])

		if err = xfrm.netLink.XfrmPolicyDel(&currentXfrmPolicyList[i]); err != nil {
			return fmt.Errorf("error deleting XFRM policy %s: %v", currentXfrmPolicyList[i], err)
		}
	}

	return nil
}
