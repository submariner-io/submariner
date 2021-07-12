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
package mtu

import (
	"k8s.io/klog"

	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/netlink"
)

type mtuHandler struct {
	event.HandlerBase
}

func NewMTUHandler() event.Handler {
	return &mtuHandler{}
}

func (h *mtuHandler) GetNetworkPlugins() []string {
	return []string{event.AnyNetworkPlugin}
}

func (h *mtuHandler) GetName() string {
	return "MTU handler"
}

func (h *mtuHandler) Init() error {
	klog.Infof("Updating configurations for Path MTU")
	configureTCPMTUProbe()

	return nil
}

func configureTCPMTUProbe() {
	mtuProbe := "2"
	baseMss := "1024"

	err := netlink.New().ConfigureTCPMTUProbe(mtuProbe, baseMss)
	if err != nil {
		klog.Warningf(err.Error())
	}
}
