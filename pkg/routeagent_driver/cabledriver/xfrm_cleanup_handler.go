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
	"github.com/submariner-io/submariner/pkg/event"
	"github.com/submariner-io/submariner/pkg/netlink"
)

type xrfmCleanup struct {
	event.HandlerBase
}

func NewXRFMCleanupHandler() event.Handler {
	return &xrfmCleanup{}
}

func (h *xrfmCleanup) GetName() string {
	return "xfrm"
}

func (h *xrfmCleanup) GetNetworkPlugins() []string {
	return []string{event.AnyNetworkPlugin}
}

func (h *xrfmCleanup) TransitionToNonGateway() error {
	logger.Info("Transitioned to non-Gateway, cleaning up the IPsec xfrm rules")

	return netlink.DeleteXfrmRules() //nolint:wrapcheck  // No need to wrap this error
}
