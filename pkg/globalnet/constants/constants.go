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

package constants

const (
	ClusterGlobalEgressIPName = "cluster-egress.submariner.io"
	EndpointClonedFrom        = "endpoints.submariner.io/cloned-from"

	SmGlobalnetIngressChain = "SUBMARINER-GN-INGRESS"
	SmGlobalnetEgressChain  = "SUBMARINER-GN-EGRESS"
	SmGlobalnetMarkChain    = "SUBMARINER-GN-MARK"

	// The following chains are added as part of GN 2.0 implementation.
	SmGlobalnetEgressChainForPods            = "SM-GN-EGRESS-PODS"
	SmGlobalnetEgressChainForHeadlessSvcPods = "SM-GN-EGRESS-HDLS-PODS"
	SmGlobalnetEgressChainForNamespace       = "SM-GN-EGRESS-NS"
	SmGlobalnetEgressChainForCluster         = "SM-GN-EGRESS-CLUSTER"

	NATTable = "nat"

	SmGlobalIP = "submariner.io/globalIp"
)
