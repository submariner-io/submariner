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
	// IPTable chains used by RouteAgent.
	SmPostRoutingChain = "SUBMARINER-POSTROUTING"
	SmInputChain       = "SUBMARINER-INPUT"
	SmForwardChain     = "SUBMARINER-FORWARD"
	PostRoutingChain   = "POSTROUTING"
	InputChain         = "INPUT"
	ForwardChain       = "FORWARD"
	MangleTable        = "mangle"
	RemoteCIDRIPSet    = "SUBMARINER-REMOTECIDRS"
	LocalCIDRIPSet     = "SUBMARINER-LOCALCIDRS"

	RouteAgentInterClusterNetworkTableID = 149

	// To support connectivity for Pods with HostNetworking on the GatewayNode, we program
	// certain routing rules in table 150. As part of these routes, we set the source-ip of
	// the egress traffic to the corresponding CNIInterfaceIP on that host.
	RouteAgentHostNetworkTableID = 150

	NATTable    = "nat"
	FilterTable = "filter"

	OvnTransitSwitchIPAnnotation = "k8s.ovn.org/node-transit-switch-port-ifaddr"
	OvnZoneAnnotation            = "k8s.ovn.org/zone-name"
)
