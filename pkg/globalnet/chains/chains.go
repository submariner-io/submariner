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

package chains

import "github.com/submariner-io/submariner/pkg/packetfilter"

const (
	SmGlobalnetIngress = "SUBMARINER-GN-INGRESS"
	SmGlobalnetEgress  = "SUBMARINER-GN-EGRESS"
	SmGlobalnetMark    = "SUBMARINER-GN-MARK"

	// The following chains were added as part of GN 2.0 implementation.

	SmGlobalnetEgressForPods            = "SM-GN-EGRESS-PODS"
	SmGlobalnetEgressForHeadlessSvcPods = "SM-GN-EGRESS-HDLS-PODS"
	SmGlobalnetEgressForHeadlessSvcEPs  = "SM-GN-EGRESS-HDLS-EPS"
	SmGlobalnetEgressForNamespace       = "SM-GN-EGRESS-NS"
	SmGlobalnetEgressForCluster         = "SM-GN-EGRESS-CLUSTER"
)

func NewGlobalnetIngress() *packetfilter.ChainIPHook {
	return &packetfilter.ChainIPHook{
		Name:     SmGlobalnetIngress,
		Type:     packetfilter.ChainTypeNAT,
		Hook:     packetfilter.ChainHookPrerouting,
		Priority: packetfilter.ChainPriorityFirst,
	}
}
